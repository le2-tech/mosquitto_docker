#include <mosquitto_broker.h>
#include <mosquitto_plugin.h>

int basic_auth_cb_c(int event, void *event_data, void *userdata);
int acl_check_cb_c(int event, void *event_data, void *userdata);

int register_basic_auth(mosquitto_plugin_id_t *id) {
    return mosquitto_callback_register(id, MOSQ_EVT_BASIC_AUTH,
        basic_auth_cb_c, NULL, NULL);
}
int unregister_basic_auth(mosquitto_plugin_id_t *id) {
    return mosquitto_callback_unregister(id, MOSQ_EVT_BASIC_AUTH,
        basic_auth_cb_c, NULL);
}
int register_acl_check(mosquitto_plugin_id_t *id) {
    return mosquitto_callback_register(id, MOSQ_EVT_ACL_CHECK,
        acl_check_cb_c, NULL, NULL);
}
int unregister_acl_check(mosquitto_plugin_id_t *id) {
    return mosquitto_callback_unregister(id, MOSQ_EVT_ACL_CHECK,
        acl_check_cb_c, NULL);
}
