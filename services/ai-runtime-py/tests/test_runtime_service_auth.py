from fastapi.testclient import TestClient

from app.main import create_app


def _controlled_payload() -> dict[str, object]:
    return {
        "request_id": "req_service_auth",
        "conversation_id": "conv_service_auth",
        "message_id": "msg_service_auth",
        "user_query": "你好",
        "model_id": "test-model",
        "config_version": "cfg_test",
        "retrieval_config_version": "rcfg_test",
        "intent": "social_greeting",
        "stream": False,
        "messages": [],
    }


def test_runtime_product_routes_require_current_or_previous_service_token(monkeypatch) -> None:
    monkeypatch.setenv("SEE_AGENT_PROFILE", "test")
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "c" * 32)
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", "p" * 32)
    client = TestClient(create_app())

    missing = client.post("/v1/chat/run", json=_controlled_payload())
    assert missing.status_code == 401
    assert missing.json()["error"]["code"] == "runtime_service_authentication_failed"

    wrong = client.post("/v1/chat/run", json=_controlled_payload(), headers={"Authorization": "Bearer wrong-secret"})
    assert wrong.status_code == 401

    current = client.post("/v1/chat/run", json=_controlled_payload(), headers={"Authorization": f"Bearer {'c' * 32}"})
    assert current.status_code == 200

    previous = client.post("/v1/chat/run", json=_controlled_payload(), headers={"Authorization": f"Bearer {'p' * 32}"})
    assert previous.status_code == 200


def test_runtime_capability_diagnostics_require_service_token(monkeypatch) -> None:
    monkeypatch.setenv("SEE_AGENT_PROFILE", "test")
    monkeypatch.setenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN", "c" * 32)
    monkeypatch.delenv("SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS", raising=False)
    client = TestClient(create_app())

    missing = client.get("/v1/diagnostics/capabilities")
    authorized = client.get("/v1/diagnostics/capabilities", headers={"Authorization": f"Bearer {'c' * 32}"})

    assert missing.status_code == 401
    assert authorized.status_code == 200
    assert authorized.json()["generation"]["provider"] == "unavailable"
