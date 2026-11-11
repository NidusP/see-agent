from __future__ import annotations

from pathlib import Path

import pytest

from tests.common.server_utils import fetch_text, find_free_port, running_process


WORKSPACE_ROOT = Path(__file__).resolve().parents[2]
WEB_ROOT = WORKSPACE_ROOT / "apps" / "web"
ADMIN_COOKIE_HEADERS = {"Cookie": "see_agent_session=test-session; see_agent_role=admin"}
KNOWLEDGE_ADMIN_COOKIE_HEADERS = {"Cookie": "see_agent_session=test-session; see_agent_role=knowledge_admin"}
USER_COOKIE_HEADERS = {"Cookie": "see_agent_session=test-session; see_agent_role=user"}


@pytest.fixture(scope="module")
def web_base_url() -> str:
    port = find_free_port()
    base_url = f"http://127.0.0.1:{port}"
    with running_process(
        ["npm", "exec", "next", "start", "--", "--hostname", "127.0.0.1", "--port", str(port)],
        cwd=WEB_ROOT,
        startup_url=f"{base_url}/api/health",
        startup_timeout_seconds=60.0,
    ):
        yield base_url


def test_chat_workspace_route_renders(web_base_url: str) -> None:
    status, html = fetch_text(f"{web_base_url}/chat", headers=USER_COOKIE_HEADERS)

    assert status == 200
    assert "智能问答" in html
    assert "新建对话" in html
    assert 'data-testid="chat-first-shell"' in html
    assert 'data-testid="chat-main"' in html
    assert 'data-testid="conversation-context-pane"' in html
    assert 'href="/knowledge"' not in html
    assert 'href="/config"' not in html


def test_workspace_redirects_unauthenticated_to_login(web_base_url: str) -> None:
    status, html = fetch_text(f"{web_base_url}/chat")

    assert status == 200
    assert "进入单智能体工作台" in html
    assert "用户名" in html


def test_conversation_detail_route_renders_citation_panel(web_base_url: str) -> None:
    status, html = fetch_text(f"{web_base_url}/chat/conv_policy_rollout", headers=USER_COOKIE_HEADERS)

    assert status == 200
    assert "智能问答" in html
    assert "新建对话" in html
    assert 'data-testid="chat-first-shell"' in html
    assert 'data-testid="chat-main"' in html
    assert 'data-testid="citation-panel"' not in html


def test_knowledge_and_config_routes_render(web_base_url: str) -> None:
    knowledge_status, knowledge_html = fetch_text(f"{web_base_url}/knowledge", headers=ADMIN_COOKIE_HEADERS)
    config_status, config_html = fetch_text(f"{web_base_url}/config", headers=ADMIN_COOKIE_HEADERS)

    assert knowledge_status == 200
    assert "系统配置" in knowledge_html
    assert "Agent 配置" in knowledge_html
    assert "文档与版本生命周期" not in knowledge_html

    assert config_status == 200
    assert "版本历史与风险提示" in config_html
    assert "Agent 配置" in config_html


def test_knowledge_admin_sees_knowledge_but_not_config(web_base_url: str) -> None:
    knowledge_status, knowledge_html = fetch_text(f"{web_base_url}/knowledge", headers=KNOWLEDGE_ADMIN_COOKIE_HEADERS)
    config_status, config_html = fetch_text(f"{web_base_url}/config", headers=KNOWLEDGE_ADMIN_COOKIE_HEADERS)

    assert knowledge_status == 200
    assert "文档与版本生命周期" in knowledge_html
    assert config_status == 200
    assert "Agent 配置" not in config_html
    assert "知识库管理工作区" in config_html
