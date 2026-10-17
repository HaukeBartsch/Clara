<?php
// The route behind `POST /mcp` (AI_Assistant_Design.md §2): the assistant's MCP endpoint. The
// router has already applied the `admin` guard and the CSRF check before this runs — the session
// is the identity, exactly as on every other page (REQ-AI-001). Everything else belongs to
// Clara\Mcp\Server; this class only bridges raw bytes and Response objects.

declare(strict_types=1);

namespace Clara\Controllers;

use Clara\Mcp\Server;
use Clara\Mcp\Tools;
use Clara\Response;

final class AssistantController extends Controller
{
    /**
     * The data-region handler the router selects for `Accept: application/json` — which every
     * MCP request carries. A parse miss stays a JSON-RPC error rather than an HTTP one so the
     * client's protocol layer sees it (design §2).
     */
    public function data(): Response
    {
        $body = file_get_contents('php://input');
        if ($body === false) {
            $body = '';
        }

        $answer = (new Server(new Tools($this->api, $this->logger)))->handle($body);
        if ($answer['payload'] === null) {
            return Response::html('', $answer['status']);
        }

        return Response::json($answer['payload'], $answer['status']);
    }

    /** A POST to /mcp without `Accept: application/json` is not an MCP request. */
    public function index(): Response
    {
        return Response::notAcceptable();
    }
}
