#!/usr/bin/env python3
"""Resume an existing Cursor agent (local or cloud) and send a follow-up.

Runtime is inferred from the agent id: ``bc-…`` → cloud; otherwise local.
Inline MCP configs are not persisted across resume — pass ``--mcp-json`` again.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any, Mapping

from cursor_sdk import (
    Agent,
    AgentOptions,
    CursorAgentError,
    LocalAgentOptions,
    SDKAssistantMessage,
    TextBlock,
    close_default_client,
)


def _require_api_key() -> str:
    key = os.environ.get("CURSOR_API_KEY", "").strip()
    if not key:
        print(
            "CURSOR_API_KEY is required (Dashboard → Integrations).",
            file=sys.stderr,
        )
        sys.exit(1)
    return key


def _load_mcp(path: str | None) -> Mapping[str, Any] | None:
    if not path:
        return None
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise SystemExit("--mcp-json must be a JSON object of server configs")
    return data


def _build_options(
    *,
    api_key: str,
    agent_id: str,
    model: str,
    cwd: str | None,
    mcp_servers: Mapping[str, Any] | None,
) -> AgentOptions:
    # Explicit local/cloud: bc- prefix is cloud; otherwise resume against cwd.
    if agent_id.startswith("bc-"):
        return AgentOptions(
            api_key=api_key,
            model=model,
            mcp_servers=mcp_servers,
        )
    return AgentOptions(
        api_key=api_key,
        model=model,
        local=LocalAgentOptions(cwd=cwd or os.getcwd()),
        mcp_servers=mcp_servers,
    )


def resume_and_send(
    agent_id: str,
    message: str,
    *,
    model: str,
    cwd: str | None,
    mcp_json: str | None,
    stream: bool,
) -> int:
    api_key = _require_api_key()
    options = _build_options(
        api_key=api_key,
        agent_id=agent_id,
        model=model,
        cwd=cwd,
        mcp_servers=_load_mcp(mcp_json),
    )

    try:
        with Agent.resume(agent_id, options) as agent:
            run = agent.send(message)
            print(f"agent_id={agent.agent_id} run_id={run.id}", flush=True)

            if stream:
                for event in run.stream():
                    if isinstance(event, SDKAssistantMessage):
                        for block in event.message.content:
                            if isinstance(block, TextBlock):
                                sys.stdout.write(block.text)
                                sys.stdout.flush()
                if stream:
                    sys.stdout.write("\n")

            result = run.wait()
            if result.status == "error":
                print(f"run failed: {result.id}", file=sys.stderr)
                return 2
            if not stream and result.result:
                print(result.result)
            print(f"status={result.status}", flush=True)
            return 0
    except CursorAgentError as err:
        print(
            f"startup failed: {err.message} retryable={err.is_retryable}",
            file=sys.stderr,
        )
        return 1
    finally:
        close_default_client()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Resume a Cursor agent and send a follow-up message.",
    )
    parser.add_argument("agent_id", help="Agent id (bc-… for cloud)")
    parser.add_argument("message", help="Follow-up prompt to send")
    parser.add_argument(
        "--model",
        default="composer-2.5",
        help="Model id (default: composer-2.5)",
    )
    parser.add_argument(
        "--cwd",
        default=None,
        help="Local agent cwd (required context for non-bc ids)",
    )
    parser.add_argument(
        "--mcp-json",
        default=None,
        help="Path to JSON object of mcp_servers to re-attach on resume",
    )
    parser.add_argument(
        "--stream",
        action="store_true",
        help="Stream assistant text while the run is active",
    )
    args = parser.parse_args(argv)
    return resume_and_send(
        args.agent_id,
        args.message,
        model=args.model,
        cwd=args.cwd,
        mcp_json=args.mcp_json,
        stream=args.stream,
    )


if __name__ == "__main__":
    raise SystemExit(main())
