from mythic_container.C2ProfileBase import *
import pathlib

version = "0.1.2"


class Websocket(C2Profile):
    name = "websocket"
    description = f"Websocket C2 Server with Poll and Push capabilities. Version {version}"
    author = "@xorrior"
    is_p2p = False
    server_binary_path = pathlib.Path(".") / "websocket" / "c2_code" / "mythic_websocket_server"
    server_folder_path = pathlib.Path(".") / "websocket" / "c2_code"
    parameters = [
        C2ProfileParameter(
            name="callback_hosts",
            description="Callback hosts (including optional port)",
            parameter_type=ParameterType.Array,
            default_value=["ws://127.0.0.1", "ws://127.0.1.1:8081"],
            verifier_regex=f"^(ws|wss)://[a-zA-Z0-9.]+(:[0-9]{2, 5})?",
            required=True,
        ),
        C2ProfileParameter(
            name="domain_rotation_method",
            description="Domain rotation method",
            parameter_type=ParameterType.ChooseOne,
            default_value="fail-over",
            choices=["fail-over", "random"],
            required=False,
        ),
        C2ProfileParameter(
            name="domain_rotation_delay",
            description="Domain rotation delay",
            parameter_type=ParameterType.Number,
            default_value=10,
            required=False,
        ),
        C2ProfileParameter(
            name="domain_rotation_failure_threshold",
            description="Domain rotation failure threshold",
            parameter_type=ParameterType.Number,
            default_value=-1,
            required=False,
        ),
        C2ProfileParameter(
            name="headers",
            description="Custom headers",
            parameter_type=ParameterType.Dictionary,
            dictionary_choices=[
                DictionaryChoice(name="user-agent",
                                 default_value="Mozilla/5.0 (Windows NT 6.3; Trident/7.0; rv:11.0) like Gecko",
                                 default_show=True,
                                 ),
                DictionaryChoice(name="host",
                                 default_value="",
                                 default_show=False,
                                 ),
            ],
            required=False,
        ),
        C2ProfileParameter(
            name="AESPSK",
            description="Crypto type",
            parameter_type=ParameterType.ChooseOne,
            default_value="aes256_hmac",
            choices=["aes256_hmac", "none"],
            required=False,
            crypto_type=True,
        ),
        C2ProfileParameter(
            name="callback_interval",
            description="Callback Interval in seconds",
            parameter_type=ParameterType.Number,
            default_value=10,
            required=False,
        ),
        C2ProfileParameter(
            name="encrypted_exchange_check",
            description="Perform Key Exchange",
            parameter_type=ParameterType.Boolean,
            default_value=True,
            required=False,
        ),
        C2ProfileParameter(
            name="endpoint",
            description="Websockets Endpoint",
            parameter_type=ParameterType.String,
            default_value="/socket",
            verifier_regex=f"^/[a-zA-Z0-9]",
            required=False,
        ),
        C2ProfileParameter(
            name="callback_jitter",
            description="Callback Jitter in percent",
            parameter_type=ParameterType.Number,
            default_value=37,
            required=False,
        ),
        C2ProfileParameter(
            name="tasking_type",
            description="'Poll' for tasking at an interval or have Mythic 'Push' new tasking as it arrives",
            parameter_type=ParameterType.ChooseOne,
            default_value="Push",
            choices=["Poll", "Push"],
            required=False
        ),
        C2ProfileParameter(
            name="killdate",
            description="Killdate for when the C2 Profile should stop working and exit the agent",
            parameter_type=ParameterType.Date,
            default_value=365,
            required=False
        ),
    ]

    async def redirect_rules(self, inputMsg: C2GetRedirectorRulesMessage) -> C2GetRedirectorRulesMessageResponse:
        """Generate Apache ModRewrite rules given the Payload's C2 configuration

        :param inputMsg: Payload's C2 Profile configuration
        :return: C2GetRedirectorRulesMessageResponse detailing some Apache ModRewrite rules for the payload
        """
        response = C2GetRedirectorRulesMessageResponse(Success=True)
        output = "########################################\n"
        output += "# You need to enable the appropriate proxy mod in apache for websocket proxying to work:\n"
        output += "# `sudo a2enmod proxy_wstunnel`"
        output += "## .htaccess START\n"
        output += "RewriteEngine On\n"
        with open("websocket/c2_code/config.json", "r") as f:
            config = json.load(f)
            for i in range(len(config["instances"])):
                url = "\"wss://" if config["instances"][i]["usessl"] else "\"ws://"
                url += f"C2_SERVER_HERE:{config['instances'][i]['bindaddress'].split(':')[1]}"
                url += "%{REQUEST_URI}\" [P,L]"
                output += "RewriteCond %{REQUEST_METHOD} ^(GET|POST) [NC]\n"
                output += f"RewriteCond %{{REQUEST_URI}} ^(/{inputMsg.Parameters['ENDPOINT_REPLACE']}.*)$\n"
                userAgent = inputMsg.Parameters['USER_AGENT'].replace('(', '\\(').replace(')', '\\)')
                output += f"RewriteCond %{{HTTP_USER_AGENT}} \"{userAgent}\"\n"
                output += "RewriteCond %{HTTP:Upgrade} websocket [NC]\n"
                output += "RewriteCond %{HTTP:Connection} upgrade [NC]\n"
                output += f"RewriteRule ^.*$ {url}\n"

        output += "RewriteRule ^.*$ redirect/? [L,R=302]\n"
        output += "## .htaccess END\n"
        output += "########################################\n"
        response.Message = output
        return response

    async def host_file(self, inputMsg: C2HostFileMessage) -> C2HostFileMessageResponse:
        """Host a file through a c2 channel

        :param inputMsg: The file UUID to host and which URL to host it at
        :return: C2HostFileMessageResponse detailing success or failure to host the file
        """
        response = C2HostFileMessageResponse(Success=False)
        try:
            config = json.load(open("websocket/c2_code/config.json", "r"))
            for i in range(len(config["instances"])):
                if "payloads" not in config["instances"][i]:
                    config["instances"][i]["payloads"] = {}
                config["instances"][i]["payloads"][inputMsg.HostURL] = inputMsg.FileUUID
            with open("websocket/c2_code/config.json", 'w') as configFile:
                configFile.write(json.dumps(config, indent=4))
            response.Success = True
            response.Message = "Successfully updated"
        except Exception as e:
            response.Error = f"{e}"
        return response
