from mythic_container.C2ProfileBase import *
import pathlib


class Websocket(C2Profile):
    name = "websocket"
    description = "Websocket C2 Server for poseidon"
    author = "@xorrior"
    is_p2p = False
    server_binary_path = pathlib.Path(".") / "websocket" / "c2_code" / "mythic_websocket_server"
    server_folder_path = pathlib.Path(".") / "websocket" / "c2_code"
    parameters = [
        C2ProfileParameter(
            name="callback_host",
            description="Callback Host",
            default_value="ws://127.0.0.1",
            verifier_regex="^(ws|wss)://[a-zA-Z0-9]+",
        ),
        C2ProfileParameter(
            name="USER_AGENT",
            description="User Agent",
            default_value="Mozilla/5.0 (Windows NT 6.3; Trident/7.0; rv:11.0) like Gecko",
            required=False,
        ),
        C2ProfileParameter(
            name="AESPSK",
            description="Crypto type",
            default_value="aes256_hmac",
            parameter_type=ParameterType.ChooseOne,
            choices=["aes256_hmac", "none"],
            required=False,
            crypto_type=True
        ),
        C2ProfileParameter(
            name="callback_interval",
            description="Callback Interval in seconds",
            default_value="10",
            verifier_regex="^[0-9]+$",
            required=False,
        ),
        C2ProfileParameter(
            name="encrypted_exchange_check",
            description="Perform Key Exchange",
            choices=["T", "F"],
            parameter_type=ParameterType.ChooseOne,
            required=False,
        ),
        C2ProfileParameter(
            name="domain_front",
            description="Host header value for domain fronting",
            default_value="",
            required=False,
        ),
        C2ProfileParameter(
            name="ENDPOINT_REPLACE",
            description="Websockets Endpoint",
            default_value="socket",
            required=False,
        ),
        C2ProfileParameter(
            name="callback_jitter",
            description="Callback Jitter in percent",
            default_value="37",
            verifier_regex="^[0-9]+$",
            required=False,
        ),
        C2ProfileParameter(
            name="callback_port",
            description="Callback Port",
            default_value="8081",
            verifier_regex="^[0-9]+$",
            required=False,
        ),
        C2ProfileParameter(
            name="tasking_type",
            description="'Poll' for tasking at an interval or have Mythic 'Push' new tasking as it arrives",
            default_value="Poll",
            choices=["Poll", "Push"],
            parameter_type=ParameterType.ChooseOne,
        ),
    ]
