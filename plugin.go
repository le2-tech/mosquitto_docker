package main

/*
#cgo darwin pkg-config: libmosquitto
#cgo darwin LDFLAGS: -Wl,-undefined,dynamic_lookup
#cgo linux  pkg-config: libmosquitto
#include <stdlib.h>
#include <mosquitto.h>
#include <mosquitto_plugin.h>
#include <mosquitto_broker.h>


typedef void* pvoid;

int register_basic_auth(mosquitto_plugin_id_t *id);
int unregister_basic_auth(mosquitto_plugin_id_t *id);
int register_acl_check(mosquitto_plugin_id_t *id);
int unregister_acl_check(mosquitto_plugin_id_t *id);
void go_mosq_log(int level, const char* msg);
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	logPrefix        = "mosq-pg"
	defaultTimeoutMS = 1500
)

var (
	pid         *C.mosquitto_plugin_id_t
	pool        *pgxpool.Pool
	pgDSN       string // postgres://user:pass@host:5432/db?sslmode=verify-full
	timeout     = time.Duration(defaultTimeoutMS) * time.Millisecond
	failOpen    bool
	enforceBind bool
	debugLogs   = envBool("MOSQ_PG_DEBUG")
)

func mosqLog(level C.int, msg string, args ...any) {
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	cs := C.CString(msg)
	defer C.free(unsafe.Pointer(cs))
	C.go_mosq_log(level, cs)
	if debugLogs {
		fmt.Fprintf(os.Stderr, "mosq-pg: %s\n", msg)
	}
}

func cstr(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}
func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
func safeDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

func parseBoolOption(v string) (value bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, true
	case "0", "false", "f", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func parseTimeoutMS(v string) (time.Duration, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}

func sha256PwdSalt(pwd, salt string) string {
	sum := sha256.Sum256([]byte(pwd + salt))
	return hex.EncodeToString(sum[:])
}

// --- Version negotiation ---
//
//export go_mosq_plugin_version
func go_mosq_plugin_version(count C.int, versions *C.int) C.int {
	for _, v := range unsafe.Slice(versions, int(count)) {
		if v == 5 {
			return 5
		}
	}
	return -1
}

// --- Init （注意：userdata 是 void**，这里用 **C.pvoid 对应）---
//
//export go_mosq_plugin_init
func go_mosq_plugin_init(id *C.mosquitto_plugin_id_t, userdata *unsafe.Pointer,
	opts *C.struct_mosquitto_opt, optCount C.int) (rc C.int) {

	defer func() {
		if r := recover(); r != nil {
			mosqLog(C.MOSQ_LOG_ERR, "mosq-pg: panic in plugin_init: %v\n%s", r, string(debug.Stack()))
			rc = C.MOSQ_ERR_UNKNOWN
		}
	}()

	pid = id

	// 读取 plugin_opt_*
	for _, o := range unsafe.Slice(opts, int(optCount)) {
		k, v := cstr(o.key), cstr(o.value)
		switch k {
		case "pg_dsn":
			pgDSN = v
		case "timeout_ms":
			if dur, ok := parseTimeoutMS(v); ok {
				timeout = dur
			} else {
				mosqLog(C.MOSQ_LOG_WARNING, "%s: invalid timeout_ms=%q, keeping existing value %dms",
					logPrefix, v, int(timeout/time.Millisecond))
			}
		case "fail_open":
			if parsed, ok := parseBoolOption(v); ok {
				failOpen = parsed
			} else {
				mosqLog(C.MOSQ_LOG_WARNING, "%s: invalid fail_open=%q, keeping existing value %t",
					logPrefix, v, failOpen)
			}
		case "enforce_bind":
			if parsed, ok := parseBoolOption(v); ok {
				enforceBind = parsed
			} else {
				mosqLog(C.MOSQ_LOG_WARNING, "%s: invalid enforce_bind=%q, keeping existing value %t",
					logPrefix, v, enforceBind)
			}
		}
	}
	if pgDSN == "" {
		mosqLog(C.MOSQ_LOG_ERR, "mosq-pg: pg_dsn must be set")
		return C.MOSQ_ERR_UNKNOWN
	}

	mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: initializing pg_dsn=%s timeout_ms=%d fail_open=%t enforce_bind=%t",
		safeDSN(pgDSN), int(timeout/time.Millisecond), failOpen, enforceBind)

	// 初始化 PG 连接池
	cfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		mosqLog(C.MOSQ_LOG_ERR, "mosq-pg: invalid pg_dsn (%s): %v", safeDSN(pgDSN), err)
		return C.MOSQ_ERR_UNKNOWN
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 60 * time.Second
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		mosqLog(C.MOSQ_LOG_ERR, "mosq-pg: pg pool init failed: %v", err)
		return C.MOSQ_ERR_UNKNOWN
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	if err = pool.Ping(ctx); err != nil {
		mosqLog(C.MOSQ_LOG_ERR, "mosq-pg: pg ping failed: %v", err)
		return C.MOSQ_ERR_UNKNOWN
	}
	mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: connected to PostgreSQL successfully")

	// 注册回调
	if rc := C.register_basic_auth(pid); rc != C.MOSQ_ERR_SUCCESS {
		return rc
	}
	if rc := C.register_acl_check(pid); rc != C.MOSQ_ERR_SUCCESS {
		C.unregister_basic_auth(pid)
		return rc
	}

	mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: plugin initialized")
	return C.MOSQ_ERR_SUCCESS
}

// --- Cleanup （void** 对应 **C.pvoid）---
//
// --- Cleanup: 头文件是 void *userdata —— 在 Go 里用 unsafe.Pointer 承接 ---
//
//export go_mosq_plugin_cleanup
func go_mosq_plugin_cleanup(userdata unsafe.Pointer, opts *C.struct_mosquitto_opt, optCount C.int) C.int {
	C.unregister_acl_check(pid)
	C.unregister_basic_auth(pid)
	if pool != nil {
		pool.Close()
	}
	mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: plugin cleaned up")
	return C.MOSQ_ERR_SUCCESS
}

// -------- BASIC_AUTH / ACL_CHECK 回调保持不变 --------

//export basic_auth_cb_c
func basic_auth_cb_c(event C.int, event_data unsafe.Pointer, userdata unsafe.Pointer) C.int {
	ed := (*C.struct_mosquitto_evt_basic_auth)(event_data)
	username, password := cstr(ed.username), cstr(ed.password)
	clientID := cstr(C.mosquitto_client_id(ed.client))

	allow, err := dbAuth(username, password, clientID)
	if err != nil {
		mosqLog(C.MOSQ_LOG_WARNING, "mosq-pg auth error: "+err.Error())
		if failOpen {
			mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: fail_open=true, allowing auth despite error")
			return C.MOSQ_ERR_SUCCESS
		}
		return C.MOSQ_ERR_AUTH
	}
	if allow {
		return C.MOSQ_ERR_SUCCESS
	}
	return C.MOSQ_ERR_AUTH
}

//export acl_check_cb_c
func acl_check_cb_c(event C.int, event_data unsafe.Pointer, userdata unsafe.Pointer) C.int {
	ed := (*C.struct_mosquitto_evt_acl_check)(event_data)
	username := cstr(C.mosquitto_client_username(ed.client))
	clientID := cstr(C.mosquitto_client_id(ed.client))
	ipAddr := cstr(C.mosquitto_client_address(ed.client))
	topic := cstr(ed.topic)
	access := int(ed.access) // READ=1, WRITE=2, SUBSCRIBE=4

	allow, err := dbACL(username, clientID, ipAddr, topic, access)
	if err != nil {
		mosqLog(C.MOSQ_LOG_WARNING, "mosq-pg acl error: "+err.Error())
		if failOpen {
			mosqLog(C.MOSQ_LOG_INFO, "mosq-pg: fail_open=true, allowing ACL despite error")
			return C.MOSQ_ERR_SUCCESS
		}
		return C.MOSQ_ERR_ACL_DENIED
	}
	if allow {
		return C.MOSQ_ERR_SUCCESS
	}
	return C.MOSQ_ERR_ACL_DENIED
}

// ----------------- PostgreSQL 逻辑（与你现有一致） -----------------

func ctxTimeout() (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), timeout)
}

func dbAuth(username, password, clientID string) (bool, error) {
	if username == "" || password == "" {
		return false, nil
	}
	ctx, cancel := ctxTimeout()
	defer cancel()

	var hash string
	var salt string
	var enabledInt int16
	err := pool.QueryRow(ctx,
		"SELECT password_hash, salt, enabled FROM iot_devices WHERE username=$1",
		username).Scan(&hash, &salt, &enabledInt)

	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if enabledInt == 0 {
		return false, nil
	}
	if hash != sha256PwdSalt(password, salt) {
		return false, nil
	}

	if enforceBind {
		var ok int
		err = pool.QueryRow(ctx,
			"SELECT 1 FROM client_bindings WHERE username=$1 AND client_id=$2",
			username, clientID).Scan(&ok)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func dbACL(username, _ string, ipAddr, topic string, access int) (bool, error) {
	isSubscribe := access&4 != 0

	// Allow dashboard user to subscribe to $SYS/#.
	if username == "dashboard" && isSubscribe && topic == "$SYS/#" {
		return true, nil
	}

	// Allow all operations for clients connected from 127.0.0.1.
	if ipAddr == "127.0.0.1" {
		return true, nil
	}

	// Deny subscriptions to critical system wildcards for everyone else.
	if isSubscribe {
		switch topic {
		case "$SYS/#", "#", "+/#":
			return false, nil
		}
	}

	// Default allow.
	return true, nil
}

func mqttMatch(pattern, topic, username, clientID string) bool {
	p := strings.ReplaceAll(pattern, "{username}", username)
	p = strings.ReplaceAll(p, "{clientid}", clientID)
	ps := strings.Split(p, "/")
	ts := strings.Split(topic, "/")
	for i := 0; i < len(ps); i++ {
		if i >= len(ts) {
			return ps[i] == "#" && i == len(ps)-1
		}
		switch ps[i] {
		case "#":
			return i == len(ps)-1
		case "+":
			// pass
		default:
			if ps[i] != ts[i] {
				return false
			}
		}
	}
	return len(ps) == len(ts)
}

func main() {}
