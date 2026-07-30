#include <gio/gio.h>
#include <libqmi-glib/libqmi-glib.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static GMainLoop *loop;
static QmiDevice *device;
static const char *pdu_hex;
static int force_ims = -1;

static void
fail(const char *stage, GError *error)
{
    fprintf(stderr, "%s: %s\n", stage, error ? error->message : "unknown error");
    g_clear_error(&error);
    g_main_loop_quit(loop);
}

static GArray *
decode_hex(const char *hex)
{
    GArray *bytes;
    size_t len = strlen(hex);

    if (len == 0 || len % 2 != 0)
        return NULL;
    bytes = g_array_sized_new(FALSE, FALSE, sizeof(guint8), len / 2);
    for (size_t i = 0; i < len; i += 2) {
        char pair[3] = {hex[i], hex[i + 1], '\0'};
        char *end = NULL;
        unsigned long value = strtoul(pair, &end, 16);
        guint8 byte = (guint8)value;
        if (!end || *end != '\0') {
            g_array_unref(bytes);
            return NULL;
        }
        g_array_append_val(bytes, byte);
    }
    return bytes;
}

static void
send_ready(QmiClientWms *client, GAsyncResult *result, gpointer unused)
{
    QmiMessageWmsRawSendOutput *output;
    QmiWmsGsmUmtsRpCause rp_cause;
    QmiWmsGsmUmtsTpCause tp_cause;
    QmiWmsMessageDeliveryFailureType failure_type;
    guint16 message_id = 0;
    GError *error = NULL;

    (void)unused;
    output = qmi_client_wms_raw_send_finish(client, result, &error);
    if (!output) {
        fail("raw-send transport", error);
        return;
    }
    if (!qmi_message_wms_raw_send_output_get_result(output, &error)) {
        fprintf(stderr, "raw-send rejected: %s\n", error ? error->message : "unknown error");
        g_clear_error(&error);
        if (qmi_message_wms_raw_send_output_get_gsm_wcdma_cause_info(
                output, &rp_cause, &tp_cause, NULL))
            fprintf(stderr, "network cause: rp=%u tp=%u\n", rp_cause, tp_cause);
        if (qmi_message_wms_raw_send_output_get_message_delivery_failure_type(
                output, &failure_type, NULL))
            fprintf(stderr, "delivery failure type: %u\n", failure_type);
        qmi_message_wms_raw_send_output_unref(output);
        g_main_loop_quit(loop);
        return;
    }
    qmi_message_wms_raw_send_output_get_message_id(output, &message_id, NULL);
    printf("raw-send accepted, message-id=%u\n", message_id);
    qmi_message_wms_raw_send_output_unref(output);
    g_main_loop_quit(loop);
}

static void
client_ready(QmiDevice *source, GAsyncResult *result, gpointer unused)
{
    QmiMessageWmsRawSendInput *input;
    QmiClient *client;
    GArray *pdu;
    GError *error = NULL;

    (void)unused;
    client = qmi_device_allocate_client_finish(source, result, &error);
    if (!client) {
        fail("allocate WMS client", error);
        return;
    }
    pdu = decode_hex(pdu_hex);
    if (!pdu) {
        fprintf(stderr, "invalid PDU hex\n");
        g_main_loop_quit(loop);
        return;
    }
    input = qmi_message_wms_raw_send_input_new();
    qmi_message_wms_raw_send_input_set_raw_message_data(
        input, QMI_WMS_MESSAGE_FORMAT_GSM_WCDMA_POINT_TO_POINT, pdu, &error);
    if (!error && force_ims >= 0)
        qmi_message_wms_raw_send_input_set_sms_on_ims(input, force_ims, &error);
    g_array_unref(pdu);
    if (error) {
        qmi_message_wms_raw_send_input_unref(input);
        fail("build raw-send request", error);
        return;
    }
    qmi_client_wms_raw_send(
        QMI_CLIENT_WMS(client), input, 150, NULL, (GAsyncReadyCallback)send_ready, NULL);
    qmi_message_wms_raw_send_input_unref(input);
}

static void
open_ready(QmiDevice *source, GAsyncResult *result, gpointer unused)
{
    GError *error = NULL;

    (void)unused;
    if (!qmi_device_open_finish(source, result, &error)) {
        fail("open QMI device", error);
        return;
    }
    qmi_device_allocate_client(
        source, QMI_SERVICE_WMS, QMI_CID_NONE, 20, NULL,
        (GAsyncReadyCallback)client_ready, NULL);
}

static void
device_ready(GObject *source, GAsyncResult *result, gpointer unused)
{
    GError *error = NULL;

    (void)source;
    (void)unused;
    device = qmi_device_new_finish(result, &error);
    if (!device) {
        fail("create QMI device", error);
        return;
    }
    qmi_device_open(
        device, QMI_DEVICE_OPEN_FLAGS_SYNC, 20, NULL,
        (GAsyncReadyCallback)open_ready, NULL);
}

int
main(int argc, char **argv)
{
    GFile *file;

    if (argc < 3 || argc > 4) {
        fprintf(stderr, "usage: %s /dev/cdc-wdm0 PDU_HEX [auto|cs|ims]\n", argv[0]);
        return 2;
    }
    pdu_hex = argv[2];
    if (argc == 4) {
        if (strcmp(argv[3], "cs") == 0)
            force_ims = 0;
        else if (strcmp(argv[3], "ims") == 0)
            force_ims = 1;
        else if (strcmp(argv[3], "auto") != 0) {
            fprintf(stderr, "invalid route: %s\n", argv[3]);
            return 2;
        }
    }
    loop = g_main_loop_new(NULL, FALSE);
    file = g_file_new_for_path(argv[1]);
    qmi_device_new(file, NULL, device_ready, NULL);
    g_object_unref(file);
    g_main_loop_run(loop);
    g_clear_object(&device);
    g_main_loop_unref(loop);
    return 0;
}
