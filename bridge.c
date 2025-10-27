#include <mosquitto.h>
#include <mosquitto_plugin.h>
#include <mosquitto_broker.h>

/* 
 * Mosquitto <-> Go 桥接层
 * -------------------------------------------------
 * 该文件提供 Mosquitto 插件的 C 侧入口，并把所有调用转发给 Go 侧实现。
 * 其核心职责包括：
 *   1. 维护 Mosquitto 期望的函数签名，避免 Go 导出符号与官方实现冲突。
 *   2. 为 Go 提供少量包装函数（如日志输出、事件注册），屏蔽 C API 的细节。
 *   3. 在需要时补充 Mosquitto 接口的形参/变参处理差异，保证二进制兼容。
 */

/* Go 导出的别名函数（避免与官方符号重名） */
int go_mosq_plugin_version(int supported_version_count,const int *supported_versions);
int go_mosq_plugin_init(mosquitto_plugin_id_t *identifier, void **userdata,
                        struct mosquitto_opt *options, int option_count);
int go_mosq_plugin_cleanup(void *userdata, struct mosquitto_opt *options, int option_count);

/* —— 官方要求的 3 个入口：C 层精确匹配原型，再转调 Go —— */
int mosquitto_plugin_version(int supported_version_count, const int *supported_versions) {
    /* 版本协商必须保持 const 签名，因此在此处做一次安全的类型转换 */
    return go_mosq_plugin_version(supported_version_count, (int*)supported_versions);
}

int mosquitto_plugin_init(mosquitto_plugin_id_t *identifier, void **userdata,
                          struct mosquitto_opt *options, int option_count) {
    /* 初始化阶段将 Mosquitto 提供的上下文原样转发给 Go 处理 */
    return go_mosq_plugin_init(identifier, userdata, options, option_count);
}

/* 这里是关键修复：cleanup 的第一个参数是 void *（单指针），不是 void ** */
int mosquitto_plugin_cleanup(void *userdata, struct mosquitto_opt *options, int option_count) {
    return go_mosq_plugin_cleanup(userdata, options, option_count);
}

/* Go 暴露的事件回调 */
int basic_auth_cb_c(int event, void *event_data, void *userdata);
int acl_check_cb_c (int event, void *event_data, void *userdata);

typedef int (*mosq_event_cb)(int event, void *event_data, void *userdata);

int register_event_callback(mosquitto_plugin_id_t *id, int event, mosq_event_cb cb) {
    /* 注册事件时无需上下文数据/清理函数，由 Go 侧统一保持状态 */
    return mosquitto_callback_register(id, event, cb, NULL, NULL);
}

int unregister_event_callback(mosquitto_plugin_id_t *id, int event, mosq_event_cb cb) {
    /* 与 register 对应，释放时只需匹配事件和回调 */
    return mosquitto_callback_unregister(id, event, cb, NULL);
}

/* 避免 Go 直接调可变参 */
void go_mosq_log(int level, const char* msg) {
    /* 保持日志格式化逻辑在 C 端处理，避免 Go 处理变参导致崩溃 */
    mosquitto_log_printf(level, "%s", msg);
}
