package main

/*
#cgo CFLAGS: -I/usr/include -I/usr/include/mosquitto
#include <stdlib.h>
#include <mosquitto_broker.h>
#include <mosquitto_plugin.h>

int basic_auth_cb_c(int event, void *event_data, void *userdata);
int acl_check_cb_c(int event, void *event_data, void *userdata);

int register_basic_auth(mosquitto_plugin_id_t *id);
int unregister_basic_auth(mosquitto_plugin_id_t *id);
int register_acl_check(mosquitto_plugin_id_t *id);
int unregister_acl_check(mosquitto_plugin_id_t *id);
*/
import "C"
import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	pid            *C.mosquitto_plugin_id_t
	pool           *pgxpool.Pool
	pgDSN          = ""     // e.g. postgres://user:pass@host:5432/db?sslmode=verify-full
	timeoutMS      = 1500   // query timeout in ms
	failOpen       = false  // if true, allow when DB fails (NOT recommended for security)
	enforceBind    = false  // if true, require username+clientID binding
)

func cstr(s *C.char) string {
	if s == nil { return "" }
	return C.GoString(s)
}

// --- Version negotiation ---
//export mosquitto_plugin_version
func mosquitto_plugin_version(count C.int, versions *C.int) C.int {
	for _, v := range unsafe.Slice(versions, int(count)) {
		if v == 5 { return 5 }
	}
	return -1
}

// --- Init ---
//export mosquitto_plugin_init
func mosquitto_plugin_init(id *C.mosquitto_plugin_id_t, userdata *unsafe.Pointer,
	opts *C.struct_mosquitto_opt, optCount C.int) C.int {
	pid = id

	// Read plugin_opt_* from mosquitto.conf
	for _, o := range unsafe.Slice(opts, int(optCount)) {
		k, v := cstr(o.key), cstr(o.value)
		switch k {
		case "pg_dsn":
			pgDSN = v
		case "timeout_ms":
			if n, err := strconv.Atoi(v); err == nil && n > 0 { timeoutMS = n }
		case "fail_open":
			failOpen = (v == "true" || v == "1" || strings.ToLower(v) == "yes")
		case "enforce_bind":
			enforceBind = (v == "true" || v == "1" || strings.ToLower(v) == "yes")
		}
	}
	if pgDSN == "" {
		C.mosquitto_log_printf(C.MOSQ_LOG_ERR, C.CString("mosq-pg: pg_dsn must be set"))
		return C.MOSQ_ERR_UNKNOWN
	}

	// Create PG pool
	cfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		C.mosquitto_log_printf(C.MOSQ_LOG_ERR, C.CString("mosq-pg: invalid pg_dsn"))
		return C.MOSQ_ERR_UNKNOWN
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 60 * time.Second
	cfg.HealthCheckPeriod = 30 * time.Second
	pool, err = pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		C.mosquitto_log_printf(C.MOSQ_LOG_ERR, C.CString("mosq-pg: pg pool init failed"))
		return C.MOSQ_ERR_UNKNOWN
	}
	if err = pool.Ping(context.Background()); err != nil {
		C.mosquitto_log_printf(C.MOSQ_LOG_ERR, C.CString("mosq-pg: pg ping failed"))
		return C.MOSQ_ERR_UNKNOWN
	}

	// Register callbacks
	if rc := C.register_basic_auth(pid); rc != C.MOSQ_ERR_SUCCESS { return rc }
	if rc := C.register_acl_check(pid); rc != C.MOSQ_ERR_SUCCESS {
		C.unregister_basic_auth(pid)
		return rc
	}
	C.mosquitto_log_printf(C.MOSQ_LOG_INFO, C.CString("mosq-pg: plugin initialized"))
	return C.MOSQ_ERR_SUCCESS
}

// --- Cleanup ---
//export mosquitto_plugin_cleanup
func mosquitto_plugin_cleanup(userdata unsafe.Pointer, opts *C.struct_mosquitto_opt, optCount C.int) C.int {
	C.unregister_acl_check(pid)
	C.unregister_basic_auth(pid)
	if pool != nil { pool.Close() }
	C.mosquitto_log_printf(C.MOSQ_LOG_INFO, C.CString("mosq-pg: plugin cleaned up"))
	return C.MOSQ_ERR_SUCCESS
}

// ========== BASIC_AUTH: connection-time decision ==========

//export basic_auth_cb_c
func basic_auth_cb_c(event C.int, event_data unsafe.Pointer, userdata unsafe.Pointer) C.int {
	ed := (*C.struct_mosquitto_evt_basic_auth)(event_data)
	username, password := cstr(ed.username), cstr(ed.password)
	clientID := cstr(C.mosquitto_client_id(ed.client))

	allow, err := dbAuth(username, password, clientID)
	if err != nil {
		C.mosquitto_log_printf(C.MOSQ_LOG_WARNING, C.CString(("mosq-pg auth error: " + err.Error())))
		if failOpen { return C.MOSQ_ERR_SUCCESS }
		return C.MOSQ_ERR_AUTH
	}
	if allow { return C.MOSQ_ERR_SUCCESS }
	return C.MOSQ_ERR_AUTH
}

// ========== ACL_CHECK: per-operation authorization ==========

//export acl_check_cb_c
func acl_check_cb_c(event C.int, event_data unsafe.Pointer, userdata unsafe.Pointer) C.int {
	ed := (*C.struct_mosquitto_evt_acl_check)(event_data)
	username := cstr(C.mosquitto_client_username(ed.client))
	clientID := cstr(C.mosquitto_client_id(ed.client))
	topic    := cstr(ed.topic)
	access   := int(ed.access) // READ=1, WRITE=2, SUBSCRIBE=4

	allow, err := dbACL(username, clientID, topic, access)
	if err != nil {
		C.mosquitto_log_printf(C.MOSQ_LOG_WARNING, C.CString(("mosq-pg acl error: " + err.Error())))
		if failOpen { return C.MOSQ_ERR_SUCCESS }
		return C.MOSQ_ERR_ACL_DENIED
	}
	if allow { return C.MOSQ_ERR_SUCCESS }
	return C.MOSQ_ERR_ACL_DENIED
}

// ----------------- PostgreSQL queries -----------------

func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
}

// Validate username/password (+ optional clientID binding)
func dbAuth(username, password, clientID string) (bool, error) {
	if username == "" || password == "" { return false, nil }
	ctx, cancel := ctxTimeout(); defer cancel()

	var hash string
	var enabled bool
	err := pool.QueryRow(ctx,
		"SELECT password_hash, enabled FROM users WHERE username=$1",
		username).Scan(&hash, &enabled)

	if errors.Is(err, pgx.ErrNoRows) { return false, nil }
	if err != nil { return false, err }
	if !enabled { return false, nil }
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false, nil
	}

	if enforceBind {
		var ok int
		err = pool.QueryRow(ctx,
			"SELECT 1 FROM client_bindings WHERE username=$1 AND client_id=$2",
			username, clientID).Scan(&ok)
		if errors.Is(err, pgx.ErrNoRows) { return false, nil }
		if err != nil { return false, err }
	}
	return true, nil
}

// Evaluate ACLs for (username, topic, access)
func dbACL(username, clientID, topic string, access int) (bool, error) {
	if username == "" || topic == "" { return false, nil }
	ctx, cancel := ctxTimeout(); defer cancel()

	rows, err := pool.Query(ctx,
		"SELECT pattern, acc FROM acls WHERE username=$1 OR username='*'",
		username)
	if err != nil { return false, err }
	defer rows.Close()

	for rows.Next() {
		var pattern string
		var acc int
		if err := rows.Scan(&pattern, &acc); err != nil { continue }
		if acc&access != 0 && mqttMatch(pattern, topic, username, clientID) {
			return true, nil
		}
	}
	return false, nil
}

// MQTT topic match with +/# and placeholders {username}/{clientid}
func mqttMatch(pattern, topic, username, clientID string) bool {
	p := strings.ReplaceAll(pattern, "{username}", username)
	p = strings.ReplaceAll(p, "{clientid}", clientID)

	ps := strings.Split(p, "/")
	ts := strings.Split(topic, "/")

	i := 0
	for i < len(ps) {
		if i >= len(ts) {
			// only match if remaining is a single trailing #
			return ps[i] == "#" && i == len(ps)-1
		}
		switch ps[i] {
		case "#":
			// # must be last, matches rest
			return i == len(ps)-1
		case "+":
			// matches any single level
		default:
			if ps[i] != ts[i] { return false }
		}
		i++
	}
	return i == len(ts)
}

func main() {}
