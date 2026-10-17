// The browser half of the Clara assistant's MCP integration (AI_Assistant_Design.md §5,
// DEV-AI-2): JSON-RPC 2.0 over fetch to this application's own `POST /mcp`, with the session
// cookie and the shell's CSRF token — exactly how every other page talks to a route, never
// through to the API itself (REQ-UI-002). Only what REQ-AI-004 names is implemented; the server
// answers simple request/response, so there is no stream or notification plumbing here.

import { csrfToken } from "../app.js"

let nextId = 1

async function rpc(method, params) {
  const response = await fetch("/mcp", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-CSRF-Token": csrfToken(),
    },
    credentials: "same-origin",
    body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }),
  })

  if (response.status === 403 || response.status === 401) {
    throw new Error("forbidden")
  }

  const text = await response.text()
  let body = {}
  if (text !== "") {
    try {
      body = JSON.parse(text)
    } catch (error) {
      throw new Error("internal")
    }
  }
  if (!response.ok) {
    throw new Error((body.error && body.error.message) || "internal")
  }
  if (body.error) {
    // A JSON-RPC error is a protocol failure, not a tool result — tool failures arrive as
    // results with isError set, which the caller reads normally.
    throw new Error(body.error.message || "protocol_error")
  }

  return body.result
}

/** The MCP handshake: initialize, then announce readiness (a notification, no response body). */
export async function initialize() {
  const answer = await rpc("initialize", {
    protocolVersion: "2025-03-26",
    clientInfo: { name: "clara-assistant-ui", version: "1.0.0" },
    capabilities: {},
  })

  await fetch("/mcp", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-CSRF-Token": csrfToken(),
    },
    credentials: "same-origin",
    body: JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }),
  })

  return answer
}

/** The tool list, MCP shape: {name, description, inputSchema, annotations}. */
export async function listTools() {
  const answer = await rpc("tools/list", {})
  return (answer && answer.tools) || []
}

/** One tools/call; resolves to the MCP result object — check `isError` on the result. */
export async function callTool(name, args) {
  return rpc("tools/call", { name, arguments: args })
}
