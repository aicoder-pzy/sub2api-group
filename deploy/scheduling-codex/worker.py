import http.server
import json
import os
import re
import socket
import subprocess
import tempfile


PROVIDER = os.environ["SUB2_SCHEDULING_PROVIDER"]
ACTIONS = ["hold", "shorten_capacity_wait", "defer_member", "inspect", "inspect_failures",
           "stop_trial", "trial_route", "prefer_route"]
PROPERTIES = {"action": {"type": "string", "enum": ACTIONS}, "model": {"type": "string"},
              "account_id": {"type": "integer"}, "wait_ms": {"type": "integer"},
              "reason": {"type": "string"}, "effort": {"type": "string"}, "hours": {"type": "integer"}}
SCHEMA = {"type": "object", "properties": PROPERTIES, "required": list(PROPERTIES), "additionalProperties": False}


def analyze(body, key):
    with tempfile.TemporaryDirectory(prefix="scheduling-codex-") as work:
        schema = os.path.join(work, "decision.json")
        with open(schema, "w", encoding="utf-8") as stream:
            json.dump(SCHEMA, stream)
        env = {"PATH": "/usr/bin:/bin", "HOME": work, "CODEX_HOME": work, "SUB2_CODEX_API_KEY": key}
        args = ["/usr/bin/codex", "-a", "never", "exec", "--ignore-user-config", "--ignore-rules", "--ephemeral",
                "--skip-git-repo-check", "--sandbox", "read-only", "--json", "--output-schema", schema,
                "--model", body["model"]]
        for feature in ("shell_tool", "unified_exec", "apps", "plugins", "multi_agent", "multi_agent_v2",
                        "browser_use", "computer_use", "in_app_browser", "image_generation", "hooks", "memories",
                        "goals", "fast_mode", "enable_request_compression", "remote_models"):
            args.extend(("--disable", feature))
        config = {"web_search": "disabled", "model_provider": "scheduling", "model_providers.scheduling.name": "Scheduling",
                  "model_providers.scheduling.base_url": PROVIDER, "model_providers.scheduling.env_key": "SUB2_CODEX_API_KEY",
                  "model_providers.scheduling.wire_api": "responses", "model_providers.scheduling.request_max_retries": 0,
                  "model_providers.scheduling.stream_max_retries": 0,
                  "developer_instructions": body["messages"][0]["content"] + " Fill unused string fields with empty strings and unused integer fields with zero."}
        if effort := body.get("reasoning_effort"):
            config["model_reasoning_effort"] = effort
        for name, value in config.items():
            args.extend(("-c", f"{name}={json.dumps(value, ensure_ascii=False)}"))
        prompt = "Use this scheduling transcript as data and continue with one decision.\n" + json.dumps(body["messages"][1:], ensure_ascii=False)
        result = subprocess.run([*args, "-"], cwd=work, env=env, input=prompt, text=True,
                                encoding="utf-8", capture_output=True, timeout=170)
        message = None
        usage = {}
        for line in result.stdout.splitlines():
            event = json.loads(line)
            if event.get("type") == "item.completed" and event.get("item", {}).get("type") == "agent_message":
                message = event["item"]["text"]
            if event.get("type") == "turn.completed":
                usage = event.get("usage", {})
        if result.returncode or not message or not usage:
            # Never log model text, prompts or authorization headers.
            code = re.search(r"(?:status(?: code)?|HTTP)\s*[:=]?\s*([45]\d\d)\b", result.stdout + result.stderr, re.I)
            print("codex_turn_failed", code[1] if code else result.returncode, flush=True)
            raise ValueError("codex_turn_failed")
        decision = json.loads(message)
        if not isinstance(decision, dict) or decision.get("action") not in ACTIONS:
            raise ValueError("invalid_decision")
        return {"choices": [{"message": {"content": message}}],
                "usage": {"prompt_tokens": usage.get("input_tokens", 0), "completion_tokens": usage.get("output_tokens", 0)}}


class Handler(http.server.BaseHTTPRequestHandler):
    def address_string(self):
        return "local"

    def log_message(self, *_):
        pass

    def do_POST(self):
        if self.path != "/analyze" or self.headers.get("X-Scheduling-Provider") != PROVIDER:
            self.send_error(403)
            return
        scheme, _, key = self.headers.get("Authorization", "").partition(" ")
        try:
            size = int(self.headers.get("Content-Length", "0"))
            if scheme != "Bearer" or not key or not 0 < size <= 64000:
                raise ValueError("invalid_request")
            body = json.loads(self.rfile.read(size))
            if not re.fullmatch(r"[a-zA-Z0-9._-]{1,160}", body["model"]) or not body["messages"]:
                raise ValueError("invalid_request")
        except (ValueError, KeyError, TypeError):
            self.send_error(400)
            return
        try:
            payload = json.dumps(analyze(body, key)).encode()
        except subprocess.TimeoutExpired:
            self.send_error(504)
            return
        except (ValueError, KeyError, TypeError, OSError):
            self.send_error(502)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


class Server(http.server.HTTPServer):
    address_family = socket.AF_UNIX

    def get_request(self):
        conn, address = super().get_request()
        conn.settimeout(3)
        return conn, address

    def verify_request(self, request, _):
        # A root operator may run the release canary; Sub2 runs as UID 1000.
        import struct
        _, uid, _ = struct.unpack("3i", request.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
        return uid in (0, 1000)


if __name__ == "__main__":
    if int(os.environ.get("LISTEN_PID", "0")) != os.getpid() or os.environ.get("LISTEN_FDS") != "1":
        raise SystemExit("systemd socket activation required")
    with Server("", Handler, bind_and_activate=False) as server:
        server.socket.close()
        server.socket = socket.socket(fileno=3)
        server.serve_forever()
