#!/usr/bin/env python3
"""Tiny mock of the OpenAI Responses API (SSE) for an isolated Codex home.
User text containing APPROVE -> a shell tool call (needs approval under
approval_policy=untrusted); a function_call_output -> final message; else 'ok'."""
import json, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
N = [0]
def sse(events):
    return ''.join(f"event: {e['type']}\ndata: {json.dumps(e)}\n\n" for e in events).encode()
def msg(text):
    N[0] += 1
    return {"type": "response.output_item.done", "item": {"type": "message", "role": "assistant", "id": f"msg_{N[0]}",
            "content": [{"type": "output_text", "text": text}]}}
class H(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *a): pass
    def do_GET(self):
        b = json.dumps({"data": [], "models": []}).encode()
        self.send_response(200); self.send_header('content-type', 'application/json'); self.send_header('content-length', str(len(b))); self.end_headers(); self.wfile.write(b)
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get('content-length', 0))) or b'{}')
        tools = [t.get('name') for t in body.get('tools', []) if isinstance(t, dict)]
        inp = body.get('input', [])
        last = inp[-1] if inp else {}
        texts = [c.get('text', '') for it in inp if it.get('role') == 'user' for c in (it.get('content') or []) if isinstance(c, dict)]
        sys.stderr.write(f"{time.strftime('%X')} POST {self.path} tools={tools} last={last.get('type')} lasttext={(texts[-1] if texts else '')[-60:]!r}\n"); sys.stderr.flush()
        N[0] += 1; rid = f"resp_{N[0]}"
        ev = [{"type": "response.created", "response": {"id": rid}}]
        if last.get('type') == 'function_call_output':
            ev.append(msg("done after tool"))
        elif texts and 'APPROVE' in texts[-1]:
            if 'shell_command' in tools: name, args = 'shell_command', {"command": "touch ember-approval-probe", "sandbox_permissions": "require_escalated", "justification": "Ember spike approval probe"}
            elif 'exec_command' in tools: name, args = 'exec_command', {"cmd": "touch ember-approval-probe", "sandbox_permissions": "require_escalated", "justification": "Ember spike approval probe"}
            else: name, args = 'shell', {"command": ["touch", "ember-approval-probe"], "sandbox_permissions": "require_escalated", "justification": "Ember spike approval probe"}
            ev.append({"type": "response.output_item.done", "item": {"type": "function_call", "name": name, "arguments": json.dumps(args), "call_id": f"call_{N[0]}"}})
        else:
            ev.append(msg("ok"))
        ev.append({"type": "response.completed", "response": {"id": rid, "usage": {"input_tokens": 1200, "input_tokens_details": {"cached_tokens": 0},
                   "output_tokens": 30, "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": 1230}}})
        b = sse(ev)
        self.send_response(200)
        self.send_header('content-type', 'text/event-stream')
        self.send_header('x-codex-primary-used-percent', '12.5'); self.send_header('x-codex-primary-window-minutes', '300')
        self.send_header('x-codex-secondary-used-percent', '40'); self.send_header('x-codex-secondary-window-minutes', '10080')
        self.send_header('content-length', str(len(b))); self.end_headers(); self.wfile.write(b)
ThreadingHTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
