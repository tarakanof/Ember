import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { describe, test } from 'node:test'

import {
  Outbox,
  Tracker,
  activityString,
  annotateTrail,
  dialogExpected,
  envFileProblem,
  failureOutcome,
  parseEnvFile,
  prependTrail,
  projectFromCwd,
  resolveConfig,
  send,
  sessionID,
  stateHome,
  stopFailureMessage,
  type Config,
  type Effect,
} from '../hooks/producer.ts'

const parity = JSON.parse(readFileSync(new URL('./parity.json', import.meta.url), 'utf8'))

const HOME = '/Users/tester'

function config(env: Record<string, string> = {}): Config {
  const r = resolveConfig({
    env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_URL: 'http://127.0.0.1:9', EMBER_TOKEN: 'tok', ...env },
    home: HOME,
  })
  assert.ok('config' in r, JSON.stringify(r))
  return r.config
}

function states(effects: Effect[]): string[] {
  return effects.map(e => (e.method === 'POST' ? `${e.body.state}:${e.body.message ?? ''}` : 'DELETE'))
}

describe('parity with the Go producer (test/parity.json)', () => {
  test('activityString', () => {
    for (const c of parity.activity) assert.equal(activityString(c.tool, c.input), c.want, c.tool)
  })
  test('stopFailureMessage', () => {
    for (const c of parity.stop_failure) assert.equal(stopFailureMessage(c.error, c.details, c.last), c.want)
  })
  test('failureOutcome', () => {
    for (const c of parity.failure_outcome) assert.equal(failureOutcome(c.error, c.interrupt), c.want)
  })
  test('trail', () => {
    for (const c of parity.prepend_trail) assert.equal(prependTrail(c.head, c.prev), c.want)
    for (const c of parity.annotate_trail) assert.equal(annotateTrail(c.trail, c.item, c.annotated, c.prepend), c.want)
  })
  test('env file', () => {
    for (const c of parity.env_file) assert.deepEqual(parseEnvFile(c.text), c.want)
  })
  test('session id', async () => {
    for (const c of parity.session_id) assert.equal(await sessionID(c.raw, c.cwd), c.want)
  })
})

describe('resolveConfig', () => {
  test('defaults the source to <EMBER_SOURCE>-mod', () => {
    assert.equal(config().source, 'mbp-mod')
  })
  test('EMBER_MOD_SOURCE wins', () => {
    assert.equal(config({ EMBER_MOD_SOURCE: 'mod1' }).source, 'mod1')
  })
  test('a placeholder source keeps the mod silent', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: 'set-me-to-this-laptop-id', EMBER_SERVER_URL: 'http://h' }, home: HOME })
    assert.ok('disabled' in r)
  })
  test('the kill switch keeps the mod silent', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_URL: 'http://h' }, home: HOME, killSwitch: true })
    assert.ok('disabled' in r)
  })
  test('auto reads the discovery cache when its preference matches', () => {
    const cache = JSON.stringify({ name: 'Ember', url: 'http://10.0.0.2:3627', prefer: 'nas' })
    const hit = resolveConfig({ env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_INSTANCE: 'nas' }, home: HOME, serverCache: cache })
    assert.ok('config' in hit && hit.config.serverURL === 'http://10.0.0.2:3627')
    const miss = resolveConfig({ env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_URL: 'auto' }, home: HOME, serverCache: cache })
    assert.ok('disabled' in miss)
  })
  test('an untrusted producer.env keeps the mod silent', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_URL: 'http://h' }, home: HOME, envFileProblem: 'permission must be 0600 (got 0644)' })
    assert.ok('disabled' in r)
  })
  test('envFileProblem matches the Go 0600 + owner rule', () => {
    assert.equal(envFileProblem('600 501\n', '501\n'), '')
    assert.match(envFileProblem('644 501', '501'), /0600/)
    assert.match(envFileProblem('640 501', '501'), /0600/)
    assert.match(envFileProblem('600 0', '501'), /owned/)
    assert.match(envFileProblem('', '501'), /0600/)
  })
  test('the token falls back to the process env', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: 'mbp', EMBER_SERVER_URL: 'http://h' }, home: HOME, processToken: 'envtok' })
    assert.ok('config' in r && r.config.token === 'envtok')
  })
  test('state home follows an absolute XDG_STATE_HOME only', () => {
    assert.equal(stateHome(HOME, '/var/state'), '/var/state')
    assert.equal(stateHome(HOME, 'relative'), `${HOME}/.local/state`)
  })
})

describe('Tracker', () => {
  test('a prompt posts the full wire body', () => {
    const t = new Tracker()
    const [e] = t.promptSubmit(config({ EMBER_SOURCE_COLOR: '#ff8800', EMBER_RATE_RESET: 'on' }), 1000, 's1', '  fix it  ', `${HOME}/Ember`)
    assert.deepEqual(e, {
      method: 'POST',
      body: {
        source: 'mbp-mod',
        tool: 'claude',
        session: 's1',
        state: 'running',
        message: 'fix it',
        source_color: '#ff8800',
        rate_reset: true,
        source_card: true,
        session_bar: true,
        project: 'Ember',
      },
    })
  })

  test('EMBER_SEND_PROJECT=0 drops the project', () => {
    const [e] = new Tracker().promptSubmit(config({ EMBER_SEND_PROJECT: '0' }), 0, 's1', 'x', `${HOME}/Ember`)
    assert.ok(e?.method === 'POST' && e.body.project === undefined)
  })

  test('tool calls build the activity trail and a failure annotates it', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    t.preToolUse(cfg, 1, 's1', 'Bash', { command: 'make' }, 'u1', HOME)
    t.toolOutcome(cfg, 's1', 'Bash', { command: 'make' }, 'exit 2')
    const [e] = t.preToolUse(cfg, 2, 's1', 'Read', { file_path: '/a/b.go' }, 'u2', HOME)
    assert.ok(e?.method === 'POST')
    assert.equal(e.body.activity, 'Read: b.go · Bash: make (exit 2)')
  })

  test('a dialog ask waits and the same call finishing resumes', () => {
    const cfg = config()
    const t = new Tracker()
    const seq = [
      ...t.promptSubmit(cfg, 0, 's1', 'go', HOME),
      ...t.preToolUse(cfg, 1, 's1', 'Bash', { command: 'rm x' }, 'u1', HOME),
      ...t.toolAsked(cfg, 2, 's1', 'Bash', { command: 'rm x' }, 'u1', true, HOME),
      ...t.permissionRequest(cfg, 3, 's1', 'Bash', { command: 'rm x' }, HOME),
      ...t.toolFinished(cfg, 4, 's1', 'Bash', 'other'),
      ...t.toolFinished(cfg, 5, 's1', 'Bash', 'u1'),
    ]
    assert.deepEqual(states(seq), ['running:go', 'running:Bash', 'waiting:approve Bash', 'running:Bash'])
  })

  test('auto mode waits on PermissionRequest, matched through the asked call', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    t.preToolUse(cfg, 1, 's1', 'Bash', { command: 'x' }, 'u1', HOME)
    assert.deepEqual(t.toolAsked(cfg, 2, 's1', 'Bash', { command: 'x' }, 'u1', false, HOME), [])
    assert.deepEqual(states(t.permissionRequest(cfg, 3, 's1', 'Bash', { command: 'x' }, HOME)), ['waiting:approve Bash'])
    assert.deepEqual(states(t.toolFinished(cfg, 4, 's1', 'Bash', 'u1')), ['running:Bash'])
  })

  test('a late permission_prompt right after the resume is dropped', () => {
    const cfg = config()
    const t = new Tracker()
    t.preToolUse(cfg, 0, 's1', 'Bash', {}, 'u1', HOME)
    t.toolAsked(cfg, 1, 's1', 'Bash', {}, 'u1', true, HOME)
    t.toolFinished(cfg, 2, 's1', 'Bash', 'u1')
    assert.deepEqual(t.notification(cfg, 10_000, 's1', 'permission_prompt', 'Claude needs your permission to use Bash', HOME), [])
    assert.deepEqual(states(t.notification(cfg, 20_000, 's1', 'permission_prompt', 'Claude needs your permission to use Bash', HOME)), [
      'waiting:Claude needs your permission to use Bash',
    ])
  })

  test('notifications follow the Go table', () => {
    const cfg = config()
    const t = new Tracker()
    assert.deepEqual(states(t.notification(cfg, 0, 's1', 'elicitation_dialog', 'pick one', HOME)), ['waiting:pick one'])
    assert.deepEqual(states(t.notification(cfg, 1, 's1', 'elicitation_complete', '', HOME)), ['running:'])
    assert.deepEqual(states(t.notification(cfg, 2, 's1', 'quota_auto_resume_disabled', '', HOME)), ['error:usage limit'])
    assert.deepEqual(t.notification(cfg, 3, 's1', 'idle_prompt', 'idle', HOME), [])
    assert.deepEqual(t.notification(cfg, 4, 's1', 'something_else', 'x', HOME), [])
  })

  test('Stop is done unless background work will wake the agent', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    assert.deepEqual(t.stop(cfg, 1, 's1', 'x', [{ type: 'subagent' }], HOME), [])
    assert.deepEqual(states(t.stop(cfg, 2, 's1', '\nAll green.\nmore', [{ type: 'shell' }], HOME)), ['done:All green.'])
  })

  test('StopFailure posts error with the label', () => {
    const [e] = new Tracker().stopFailure(config(), 0, 's1', 'rate_limit', '', '', HOME)
    assert.ok(e?.method === 'POST' && e.body.state === 'error' && e.body.message === 'rate limited')
  })

  test('SessionEnd deletes for the Go reasons only', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    assert.deepEqual(t.sessionEnd(cfg, 's1', 'bypass_permissions_disabled'), [])
    assert.deepEqual(t.sessionEnd(cfg, 's1', 'clear'), [
      { method: 'DELETE', body: { source: 'mbp-mod', tool: 'claude', session: 's1' } },
    ])
    assert.equal(t.sessions.size, 0)
  })

  test('a resumed or cleared SessionStart forgets the session; startup keeps it', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    t.sessionStart('s1', 'startup')
    assert.equal(t.sessions.size, 1)
    t.sessionStart('s1', 'resume')
    assert.equal(t.sessions.size, 0)
  })

  test('the heartbeat re-posts live sessions and done ones for the done TTL', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 'run', 'go', HOME)
    t.stop(cfg, 0, 'done', 'ok', [], HOME)
    assert.deepEqual(states(t.tick(cfg, 10_000)), ['running:go', 'done:ok'])
    assert.deepEqual(states(t.tick(cfg, 30_000)), ['running:go'])
  })

  test('a notification wait outliving the backstop is deleted until the next event', () => {
    const cfg = config()
    const t = new Tracker()
    t.notification(cfg, 0, 's1', 'elicitation_dialog', 'pick', HOME)
    assert.deepEqual(states(t.tick(cfg, 30 * 60_000)), ['DELETE'])
    assert.deepEqual(t.tick(cfg, 30 * 60_000 + 10_000), [])
    assert.deepEqual(states(t.promptSubmit(cfg, 31 * 60_000, 's1', 'again', HOME)), ['running:again'])
  })

  test('a permission wait is not backstopped', () => {
    const cfg = config()
    const t = new Tracker()
    t.toolAsked(cfg, 0, 's1', 'Bash', {}, 'u1', true, HOME)
    assert.deepEqual(states(t.tick(cfg, 31 * 60_000)), ['waiting:approve Bash'])
  })

  test('a session silent past EMBER_HEARTBEAT_TTL_HOURS is deleted and forgotten', () => {
    const cfg = config({ EMBER_HEARTBEAT_TTL_HOURS: '1' })
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's1', 'go', HOME)
    assert.deepEqual(states(t.tick(cfg, 3_600_000)), ['DELETE'])
    assert.equal(t.sessions.size, 0)
  })
})

test('projectFromCwd drops the home directory', () => {
  assert.equal(projectFromCwd(HOME, HOME), '')
  assert.equal(projectFromCwd(`${HOME}/Github/Ember/`, HOME), 'Ember')
  assert.equal(projectFromCwd('', HOME), '')
})

test('dialogExpected only for interactive, prompting modes', () => {
  assert.equal(dialogExpected(true, ''), true)
  assert.equal(dialogExpected(true, 'acceptEdits'), true)
  assert.equal(dialogExpected(true, 'auto'), false)
  assert.equal(dialogExpected(true, 'bypassPermissions'), false)
  assert.equal(dialogExpected(false, 'default'), false)
})

test('send POSTs and DELETEs /v1/status with the bearer token to a local stub', async () => {
  const seen: { method: string; url: string; auth: string; type: string; body: unknown }[] = []
  const server = createServer((req, res) => {
    let raw = ''
    req.on('data', chunk => (raw += chunk))
    req.on('end', () => {
      seen.push({
        method: req.method ?? '',
        url: req.url ?? '',
        auth: req.headers.authorization ?? '',
        type: req.headers['content-type'] ?? '',
        body: JSON.parse(raw),
      })
      res.writeHead(req.method === 'DELETE' ? 204 : 200, { 'content-type': 'application/json' })
      res.end(req.method === 'DELETE' ? '' : '{"ok":true}')
    })
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  try {
    const port = (server.address() as AddressInfo).port
    const cfg = config({ EMBER_SERVER_URL: `http://127.0.0.1:${port}/` })
    const t = new Tracker()
    const fetcher = (url: string, init: { method: string; headers: Record<string, string>; body: string }) => fetch(url, init)
    for (const e of t.promptSubmit(cfg, 0, 's1', 'go', HOME)) assert.equal(await send(fetcher, cfg, e), true)
    for (const e of t.sessionEnd(cfg, 's1', 'logout')) assert.equal(await send(fetcher, cfg, e), true)
    assert.deepEqual(seen, [
      {
        method: 'POST',
        url: '/v1/status',
        auth: 'Bearer tok',
        type: 'application/json',
        body: { source: 'mbp-mod', tool: 'claude', session: 's1', state: 'running', message: 'go', source_card: true, session_bar: true },
      },
      {
        method: 'DELETE',
        url: '/v1/status',
        auth: 'Bearer tok',
        type: 'application/json',
        body: { source: 'mbp-mod', tool: 'claude', session: 's1' },
      },
    ])
  } finally {
    await new Promise(resolve => server.close(resolve))
  }
})

test('send reports a refused connection as false instead of throwing', async () => {
  const cfg = config({ EMBER_SERVER_URL: 'http://127.0.0.1:9' })
  const effect: Effect = { method: 'DELETE', body: { source: 'x', tool: 'claude', session: 's' } }
  const failing = async () => {
    throw new Error('ECONNREFUSED')
  }
  assert.equal(await send(failing, cfg, effect), false)
})

const transitions = JSON.parse(readFileSync(new URL('./transitions.json', import.meta.url), 'utf8'))

type Step = { at: number; event: string; input?: Record<string, any>; want: unknown[] }

function drive(t: Tracker, cfg: Config, step: Step): Effect[] {
  const now = step.at * 1000
  const i = step.input ?? {}
  const id = i.session_id ?? ''
  switch (step.event) {
    case 'tick':
      return t.tick(cfg, now)
    case 'session-start':
      return t.sessionStart(id, i.source)
    case 'user-prompt-submit':
      return t.promptSubmit(cfg, now, id, i.prompt, i.cwd)
    case 'pre-tool-use':
      return t.preToolUse(cfg, now, id, i.tool_name, i.tool_input, i.tool_use_id, i.cwd)
    case 'permission-request':
      return t.permissionRequest(cfg, now, id, i.tool_name, i.tool_input, i.cwd)
    case 'post-tool-use':
      return t.toolFinished(cfg, now, id, i.tool_name, i.tool_use_id)
    case 'post-tool-use-failure':
      return [
        ...t.toolOutcome(cfg, id, i.tool_name, i.tool_input, failureOutcome(i.error, i.is_interrupt === true)),
        ...t.toolFinished(cfg, now, id, i.tool_name, i.tool_use_id),
      ]
    case 'permission-denied':
      return [...t.toolOutcome(cfg, id, i.tool_name, i.tool_input, 'denied'), ...t.toolFinished(cfg, now, id, i.tool_name, i.tool_use_id)]
    case 'notification':
      return t.notification(cfg, now, id, i.notification_type, i.message ?? '', i.cwd)
    case 'stop':
      return t.stop(cfg, now, id, i.last_assistant_message ?? '', i.background_tasks, i.cwd)
    case 'stop-failure':
      return t.stopFailure(cfg, now, id, i.error ?? '', i.error_details ?? '', i.last_assistant_message ?? '', i.cwd)
    case 'session-end':
      return t.sessionEnd(cfg, id, i.reason)
  }
  throw new Error(`fixture event ${step.event} has no mod mapping`)
}

describe('shared transitions with the Go producer (test/transitions.json)', () => {
  for (const sc of transitions.scenarios) {
    test(sc.name, () => {
      const env = parseEnvFile(transitions.env)
      const r = resolveConfig({ env: { ...env, EMBER_MOD_SOURCE: env.EMBER_SOURCE ?? '', EMBER_SERVER_URL: 'http://h' }, home: '/home/nobody' })
      assert.ok('config' in r)
      const t = new Tracker()
      sc.steps.forEach((step: Step, n: number) => {
        assert.deepEqual(drive(t, r.config, step), step.want, `step ${n} (${step.event} at ${step.at}s)`)
      })
    })
  }
})

describe('Outbox', () => {
  test('runs one session strictly in order: a DELETE waits for the POST in flight', async () => {
    const box = new Outbox()
    const log: string[] = []
    let release: () => void = () => {}
    const held = new Promise<void>(resolve => (release = resolve))
    void box.push('s1', async () => {
      log.push('POST start')
      await held
      log.push('POST done')
    })
    const deleted = box.push('s1', async () => {
      log.push('DELETE start')
    })
    await new Promise(resolve => setTimeout(resolve, 5))
    assert.deepEqual(log, ['POST start'])
    release()
    await deleted
    assert.deepEqual(log, ['POST start', 'POST done', 'DELETE start'])
    assert.equal(box.busy, 0)
  })

  test('coalesces queued sends so a dead server never piles them up', async () => {
    const box = new Outbox()
    const sent: number[] = []
    let release: () => void = () => {}
    const held = new Promise<void>(resolve => (release = resolve))
    void box.push('s1', async () => {
      sent.push(0)
      await held
    })
    for (let n = 1; n <= 50; n++) void box.push('s1', async () => void sent.push(n))
    release()
    await box.push('s1', async () => void sent.push(99))
    assert.deepEqual(sent, [0, 99])
  })

  test('sessions do not wait on each other', async () => {
    const box = new Outbox()
    void box.push('stuck', () => new Promise(() => {}))
    let ran = false
    await box.push('other', async () => void (ran = true))
    assert.equal(ran, true)
    assert.equal(box.busy, 1)
  })

  test('a failing send does not stop the next one', async () => {
    const box = new Outbox()
    let ran = false
    void box.push('s1', async () => {
      throw new Error('boom')
    })
    await box.push('s1', async () => void (ran = true))
    assert.equal(ran, true)
  })
})
