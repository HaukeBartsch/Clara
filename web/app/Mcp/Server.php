<?php
// The same-origin MCP endpoint of the Clara assistant (AI_Assistant_Design.md §2, REQ-AI-004):
// a minimal JSON-RPC 2.0 handler for exactly the methods the assistant's browser client uses —
// `initialize`, `notifications/initialized`, `ping`, `tools/list`, `tools/call` — as plain
// request/response with no server-initiated messages (REQ-AI-005). The hand-written framing is
// DEV-AI-2: an SDK would need a build step the stack forbids, and this subset is small enough to
// hold in one file. Statelessness is deliberate — chat history lives in the browser only.

declare(strict_types=1);

namespace Clara\Mcp;

final class Server
{
    /** The protocol revision answered when the client asks for a newer or older one. */
    private const PROTOCOL_VERSION = '2025-03-26';

    /** Request bodies are capped: a tool call carries arguments, not documents (design §2). */
    public const MAX_BODY_BYTES = 262_144;

    public function __construct(private readonly Tools $tools) {}

    /**
     * Handles one request body and answers `{status, payload}`. A notification (no `id`) gets
     * 202 with no payload, per the streamable-HTTP convention; every real method answers 200.
     *
     * @return array{status: int, payload: ?array<string, mixed>}
     */
    public function handle(string $body): array
    {
        if (strlen($body) > self::MAX_BODY_BYTES) {
            return ['status' => 413, 'payload' => self::error(null, -32600, 'request too large')];
        }

        $request = json_decode($body, true);
        if (!is_array($request)) {
            return ['status' => 200, 'payload' => self::error(null, -32700, 'parse error')];
        }
        if (($request['jsonrpc'] ?? '') !== '2.0' || !isset($request['method']) || !is_string($request['method'])) {
            return ['status' => 200, 'payload' => self::error($request['id'] ?? null, -32600, 'invalid request')];
        }

        $id = $request['id'] ?? null;
        $isNotification = !array_key_exists('id', $request);
        $params = is_array($request['params'] ?? null) ? $request['params'] : [];

        switch ($request['method']) {
            case 'initialize':
                return ['status' => 200, 'payload' => self::result($id, [
                    'protocolVersion' => self::PROTOCOL_VERSION,
                    'capabilities' => ['tools' => new \stdClass()],
                    'serverInfo' => ['name' => 'clara-assistant', 'version' => '1.0.0'],
                ])];

            case 'notifications/initialized':
            case 'notifications/cancelled':
                return $isNotification
                    ? ['status' => 202, 'payload' => null]
                    : ['status' => 200, 'payload' => self::result($id, new \stdClass())];

            case 'ping':
                return ['status' => 200, 'payload' => self::result($id, new \stdClass())];

            case 'tools/list':
                $tools = [];
                foreach ($this->tools->definitions() as $definition) {
                    unset($definition['x_mode']); // the gate is server-side; the list is protocol shape
                    $tools[] = $definition;
                }

                return ['status' => 200, 'payload' => self::result($id, ['tools' => $tools])];

            case 'tools/call':
                $name = (string) ($params['name'] ?? '');
                $arguments = is_array($params['arguments'] ?? null) ? $params['arguments'] : [];
                if ($name === '') {
                    return ['status' => 200, 'payload' => self::error($id, -32602, 'tools/call needs a name')];
                }

                return ['status' => 200, 'payload' => self::result($id, $this->tools->call($name, $arguments))];

            default:
                return ['status' => 200, 'payload' => self::error($id, -32601, 'method not found: ' . $request['method'])];
        }
    }

    /** @return array<string, mixed> */
    private static function result(mixed $id, mixed $result): array
    {
        return ['jsonrpc' => '2.0', 'id' => $id, 'result' => $result];
    }

    /** @return array<string, mixed> */
    private static function error(mixed $id, int $code, string $message): array
    {
        return ['jsonrpc' => '2.0', 'id' => $id, 'error' => ['code' => $code, 'message' => $message]];
    }
}
