from __future__ import annotations

import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


_CONTEXT_RESOLUTION_PROMPT_PREFIX = (
    "Return only one JSON object with exactly intent, route, kind, "
    "resolved_query, basis_message_ids, confidence."
)
_CONTEXT_RESOLUTION_PAYLOAD_KEYS = frozenset({"history", "current_question"})


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path.rstrip("/") != "/v1/models":
            self.send_error(404)
            return
        self._send_json({"object": "list", "data": [{"id": "llama3.2:latest", "object": "model"}]})

    def do_POST(self) -> None:
        if self.path.rstrip("/") != "/v1/chat/completions":
            self.send_error(404)
            return
        content_length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(content_length) or b"{}")
        answer = _answer_for(payload)
        if payload.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for start in range(0, len(answer), 12):
                chunk = {
                    "choices": [
                        {
                            "index": 0,
                            "delta": {"content": answer[start : start + 12]},
                            "finish_reason": None,
                        }
                    ]
                }
                self.wfile.write(f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n".encode("utf-8"))
            self.wfile.write(b"data: [DONE]\n\n")
            return

        self._send_json(
            {
                "id": "test-completion",
                "object": "chat.completion",
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": answer},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30},
            }
        )

    def log_message(self, format: str, *args: object) -> None:
        return

    def _send_json(self, payload: object) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def _answer_for(payload: dict[str, object]) -> str:
    messages = payload.get("messages") or []
    context_request = _context_resolution_request(messages)
    if context_request is not None:
        return _context_resolution_answer(context_request)

    serialized = json.dumps(messages, ensure_ascii=False)
    if "73491" in serialized:
        return "根据检索到的资料，Zeta vault emergency drill passcode is 73491。[[cite:1]]"
    if "10 calendar days" in serialized or "10个自然日" in serialized or "10 个自然日" in serialized:
        return "根据检索到的资料，差旅结束后10个自然日内提交报销。[[cite:1]]"
    return "根据检索到的测试资料，可以确认该事项。[[cite:1]]"


def _context_resolution_request(messages: object) -> dict[str, object] | None:
    if not isinstance(messages, list) or len(messages) != 2:
        return None
    system_message, user_message = messages
    if not isinstance(system_message, dict) or not isinstance(user_message, dict):
        return None
    if system_message.get("role") != "system" or user_message.get("role") != "user":
        return None
    system_content = system_message.get("content")
    user_content = user_message.get("content")
    if not isinstance(system_content, str) or not system_content.startswith(_CONTEXT_RESOLUTION_PROMPT_PREFIX):
        return None
    if not isinstance(user_content, str):
        return None
    try:
        request = json.loads(user_content)
    except json.JSONDecodeError:
        return None
    if not isinstance(request, dict) or set(request) != _CONTEXT_RESOLUTION_PAYLOAD_KEYS:
        return None
    if not isinstance(request["history"], list) or not isinstance(request["current_question"], str):
        return None
    return request


def _context_resolution_answer(request: dict[str, object]) -> str:
    question = request["current_question"]
    history = request["history"]
    if question == "真的吗？" and any(
        isinstance(message, dict)
        and message.get("role") == "assistant"
        and isinstance(message.get("content"), str)
        and "RAG" in message["content"]
        for message in history
    ):
        decision = {
            "intent": "capability_help",
            "route": "controlled_reply",
            "kind": "standalone",
            "resolved_query": None,
            "basis_message_ids": [],
            "confidence": 0.95,
        }
    else:
        decision = {
            "intent": "knowledge_query",
            "route": "rag",
            "kind": "standalone",
            "resolved_query": None,
            "basis_message_ids": [],
            "confidence": 0.95,
        }
    return json.dumps(decision, ensure_ascii=False, separators=(",", ":"))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
