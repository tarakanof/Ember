import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { describe, test } from 'node:test'

import {
  FieldDropper,
  Outbox,
  Sender,
  bounded,
  Tracker,
  activityString,
  annotateTrail,
  dialogExpected,
  envFileProblem,
  failureOutcome,
  gaugeWire,
  gaugesFromUsage,
  normalizeHostName,
  agentsCLIUsable,
  parseAgents,
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
  parseSnapshot,
  STRAY_SKIP_MS,
  unknownField,
} from '../hooks/producer.ts'
import { argvFromPs, detectT3, isT3ServerArgv, parsePsInfo, type T3Probe } from '../hooks/t3.ts'

const row = (status: string, waitingFor = '') => ({ status, waitingFor })

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

const NO_GAUGES = { contextPct: -1, ratePct: -1, rateResetAt: 0, rateResetLabel: '' }

function inZone<T>(tz: string, work: () => T): T {
  const prev = process.env.TZ
  process.env.TZ = tz
  try {
    return work()
  } finally {
    if (prev === undefined) delete process.env.TZ
    else process.env.TZ = prev
  }
}

function states(effects: Effect[]): string[] {
  return effects.map(e => (e.method === 'POST' ? `${e.body.state}:${e.body.message ?? ''}` : 'DELETE'))
}

describe('parity with the Go producer (test/parity.json)', () => {
  test('gauges: a SessionUsage reading maps to the wire fields the statusline gives Go', () => {
    assert.ok(parity.gauges.length > 5)
    for (const c of parity.gauges) {
      const got = inZone(c.tz, () => gaugeWire({ ...NO_GAUGES, ...gaugesFromUsage(c.usage) }, config(c.env).contextPct))
      assert.deepEqual(got, c.want, c.name)
    }
  })
  test('activityString', () => {
    for (const c of parity.activity) assert.equal(activityString(c.tool, c.input), c.want, c.tool)
  })
  test('parseAgents', () => {
    for (const c of parity.agents_parse) {
      const got = parseAgents(c.text)
      assert.deepEqual(got === undefined ? null : Object.fromEntries([...got].map(([id, r]) => [id, r.status])), c.want, c.name)
    }
  })
  test('a busy row ends a dialog wait only', () => {
    const cfg = config()
    for (const c of parity.agents_wait) {
      const t = new Tracker()
      t.promptSubmit(cfg, 0, 's', 'go', '/repo')
      if (c.pending) t.permissionRequest(cfg, 100, 's', 'Bash', {}, '/repo')
      else t.notification(cfg, 100, 's', 'quota_auto_resume_stale', c.message, '/repo')
      if (c.state === 'running') t.preToolUse(cfg, 100, 's', 'Bash', {}, 'toolu_1', '/repo')
      const rows = c.status === '' ? new Map() : new Map([['s', row(c.status)]])
      t.agentsCorrect(cfg, 5000, 5000, rows)
      const got = t.agentsCorrect(cfg, 7000, 7000, rows)
      const want = c.want === null ? [] : [{ state: c.want.state, message: c.want.message }]
      assert.deepEqual(got.map(e => (e.method === 'POST' ? { state: e.body.state, message: e.body.message } : e)), want, c.name)
    }
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
  test('unknownField', () => {
    for (const c of parity.unknown_field) assert.equal(unknownField(c.status, c.body), c.want, c.body)
  })
  test('host name to source', () => {
    for (const c of parity.host_name) assert.equal(normalizeHostName(c.raw), c.want, JSON.stringify(c.raw))
  })
})

type FakeProc = { ppid: number; comm: string; argv?: string[] }

function fakeProbe(env: Record<string, string>, procs: Record<string, FakeProc>, lookups: { n: number } = { n: 0 }): T3Probe {
  return {
    isSDK: async () => env.CLAUDE_CODE_ENTRYPOINT === 'sdk-ts' || (env.CLAUDE_AGENT_SDK_VERSION ?? '') !== '',
    start: async () => 100,
    info: async pid => {
      lookups.n++
      const p = procs[String(pid)]
      return p ? { ppid: p.ppid, comm: p.comm } : undefined
    },
    argv: async pid => {
      lookups.n++
      return procs[String(pid)]?.argv
    },
  }
}

describe('T3 spawn detection (#216), parity with the Go producer', () => {
  test('the shared cases (parity.json t3)', async () => {
    assert.ok(parity.t3.length > 20)
    for (const c of parity.t3) assert.equal((await detectT3(fakeProbe(c.env, c.procs))) === 'yes', c.want, c.name)
  })
  test('a failed lookup is unknown, not a verdict, so the next event tries again', async () => {
    const sdk = { CLAUDE_CODE_ENTRYPOINT: 'sdk-ts' }
    assert.equal(await detectT3(fakeProbe(sdk, {})), 'unknown')
    assert.equal(await detectT3(fakeProbe(sdk, { '100': { ppid: 99, comm: 'sh' }, '99': { ppid: 98, comm: 'claude' }, '98': { ppid: 1, comm: 'x' } })), 'unknown')
  })
  test('an interactive session pays for no process lookups', async () => {
    const lookups = { n: 0 }
    await detectT3(fakeProbe({ CLAUDE_CODE_ENTRYPOINT: 'cli' }, {}, lookups))
    assert.equal(lookups.n, 0)
  })
  test('an SDK session costs sh, claude, launcher and its argv, as in Go', async () => {
    const lookups = { n: 0 }
    const d = '/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)'
    const tree = {
      '100': { ppid: 99, comm: '/bin/sh' },
      '99': { ppid: 98, comm: 'claude' },
      '98': { ppid: 1, comm: d, argv: [d, 'serve'] },
    }
    assert.equal(await detectT3(fakeProbe({ CLAUDE_CODE_ENTRYPOINT: 'sdk-ts' }, tree, lookups)), 'no')
    assert.equal(lookups.n, 4)
    lookups.n = 0
    assert.equal(await detectT3(fakeProbe({ CLAUDE_CODE_ENTRYPOINT: 'sdk-ts' }, { ...tree, '98': { ppid: 1, comm: 't3', argv: ['/x/t3', 'serve'] } }, lookups)), 'yes')
    assert.equal(lookups.n, 4)
  })
  test('argv rebuilt from ps keeps a spaced executable whole', () => {
    const comm = '/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)'
    const args = `${comm} --require /x/compileCache.cjs /Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/server/dist/bin.mjs --bootstrap-fd 3\n`
    const argv = argvFromPs(comm, args)
    assert.equal(argv[0], comm)
    assert.equal(isT3ServerArgv(argv), true)
    assert.deepEqual(argvFromPs('/usr/bin/node', 'node /usr/local/bin/t3 serve'), ['node', '/usr/local/bin/t3', 'serve'])
    assert.equal(isT3ServerArgv(argvFromPs('/usr/bin/node', 'node /usr/local/bin/t3 serve')), true)
    assert.equal(isT3ServerArgv(argvFromPs('/usr/bin/python3', '/usr/bin/python3 t3')), false)
  })
  test('ps output parses with a spaced comm and rejects junk', () => {
    assert.deepEqual(parsePsInfo('  412 /Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)\n'), {
      ppid: 412,
      comm: '/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)',
    })
    assert.deepEqual(parsePsInfo('412  T3   Code  (Alpha)\n'), { ppid: 412, comm: 'T3 Code (Alpha)' })
    assert.equal(parsePsInfo(''), undefined)
    assert.equal(parsePsInfo('PPID COMMAND'), undefined)
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
  test('a placeholder source resolves the host id like Go', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: 'set-me-to-this-laptop-id', EMBER_SERVER_URL: 'http://h' }, home: HOME, hostName: 'Dmitrys-MacBook-Pro.local' })
    assert.ok('config' in r)
    assert.equal(r.config.source, 'mbp-mod')
    const empty = resolveConfig({ env: { EMBER_SERVER_URL: 'http://h' }, home: HOME, hostName: 'studio-box' })
    assert.ok('config' in empty)
    assert.equal(empty.config.source, 'studio-box-mod')
  })
  test('a placeholder source with no resolvable host stays silent', () => {
    const r = resolveConfig({ env: { EMBER_SOURCE: '', EMBER_SERVER_URL: 'http://h' }, home: HOME, hostName: '---' })
    assert.ok('disabled' in r)
  })
  test('EMBER_MOD_SOURCE ignores the host name', () => {
    const r = resolveConfig({ env: { EMBER_MOD_SOURCE: 'mod1', EMBER_SERVER_URL: 'http://h' }, home: HOME, hostName: 'studio-box' })
    assert.ok('config' in r)
    assert.equal(r.config.source, 'mod1')
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

describe('send against an older server', () => {
  const project = (t: Tracker, cfg: Config) => {
    const [e] = t.promptSubmit(cfg, 0, 's1', 'go', `${HOME}/Ember`)
    assert.ok(e && e.method === 'POST')
    assert.equal(e.body.project, 'Ember')
    return e
  }
  const strict = (calls: Record<string, unknown>[], rejects: string) => async (_url: string, init: { body: string }) => {
    const body = JSON.parse(init.body) as Record<string, unknown>
    calls.push(body)
    if (rejects in body) return { ok: false, status: 400, text: `{"error":"json: unknown field \\"${rejects}\\""}` }
    return { ok: true, status: 200, text: '{"ok":true}' }
  }

  test('resends without project and remembers it for an hour', async () => {
    const cfg = config()
    const e = project(new Tracker(), cfg)
    const dropper = new FieldDropper()
    const calls: Record<string, unknown>[] = []
    assert.equal(await send(strict(calls, 'project'), cfg, e, dropper, () => 1000), true)
    assert.deepEqual(calls.map(c => 'project' in c), [true, false])
    assert.equal(await send(strict(calls, 'project'), cfg, e, dropper, () => 2000), true)
    assert.deepEqual(calls.map(c => 'project' in c), [true, false, false])
    assert.equal(await send(strict(calls, 'project'), cfg, e, dropper, () => 1000 + 3_600_000), true)
    assert.deepEqual(calls.map(c => 'project' in c), [true, false, false, true, false])
  })
  test('does not retry other 400s or fields it cannot drop', async () => {
    const cfg = config()
    const e = project(new Tracker(), cfg)
    const calls: Record<string, unknown>[] = []
    const other = async (_url: string, init: { body: string }) => {
      calls.push(JSON.parse(init.body))
      return { ok: false, status: 400, text: '{"error":"state is required"}' }
    }
    assert.equal(await send(other, cfg, e, new FieldDropper(), () => 0), false)
    assert.equal(calls.length, 1)
    calls.length = 0
    assert.equal(await send(strict(calls, 'source_card'), cfg, e, new FieldDropper(), () => 0), false)
    assert.equal(calls.length, 1)
  })
  test('gives up after one resend when the server keeps rejecting', async () => {
    const cfg = config()
    const e = project(new Tracker(), cfg)
    const calls: Record<string, unknown>[] = []
    const always = async (_url: string, init: { body: string }) => {
      calls.push(JSON.parse(init.body))
      return { ok: false, status: 400, text: 'json: unknown field "project"' }
    }
    assert.equal(await send(always, cfg, e, new FieldDropper(), () => 0), false)
    assert.equal(calls.length, 2)
  })
  test('a DELETE is sent once and untouched', async () => {
    const cfg = config()
    const calls: Record<string, unknown>[] = []
    const d: Effect = { method: 'DELETE', body: { source: 'x', tool: 'claude', session: 's' } }
    assert.equal(await send(strict(calls, 'project'), cfg, d, new FieldDropper(), () => 0), true)
    assert.equal(calls.length, 1)
  })
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
    case 'statusline':
      return t.measure(id, inZone(transitions.tz, () => gaugesFromUsage(i.usage)))
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

describe('gauges on the tracker', () => {
  const reading = {
    context: { window: 200000, percent: 30 },
    rateLimits: [{ kind: 'five_hour', percentUsed: 12, resetsAt: '2026-05-12T19:37:13Z' }],
  }
  const gauges = (effects: Effect[]) =>
    effects.map(e => (e.method === 'POST' ? [e.body.context_pct, e.body.rate_window_pct, e.body.rate_reset_at] : 'DELETE'))
  test('a reading for a session the tracker does not hold is dropped, as Go drops a render with no marker', () => {
    const cfg = config()
    const t = new Tracker()
    t.measure('s1', gaugesFromUsage(reading))
    assert.deepEqual(gauges(t.promptSubmit(cfg, 1000, 's1', 'go', '/repo')), [[undefined, undefined, undefined]])
  })
  test('a resumed or cleared session starts with no gauges, as Go removes the marker', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 1000, 's1', 'go', '/repo')
    t.measure('s1', gaugesFromUsage(reading))
    t.sessionStart('s1', 'resume')
    assert.deepEqual(gauges(t.promptSubmit(cfg, 2000, 's1', 'again', '/repo')), [[undefined, undefined, undefined]])
  })
  test('EMBER_CONTEXT_PCT_ENABLED off keeps the rate gauges', () => {
    const cfg = config({ EMBER_CONTEXT_PCT_ENABLED: 'off' })
    const t = new Tracker()
    t.promptSubmit(cfg, 1000, 's1', 'go', '/repo')
    t.measure('s1', gaugesFromUsage(reading))
    assert.deepEqual(gauges(t.tick(cfg, 2000)), [[undefined, 12, 1778614633]])
  })
  test('wantsGauges asks once per tracked session with no gauges, and again after the session is forgotten', () => {
    const cfg = config()
    const t = new Tracker()
    assert.equal(t.wantsGauges('s1'), false)
    t.promptSubmit(cfg, 1000, 's1', 'go', '/repo')
    assert.equal(t.wantsGauges('s1'), true)
    assert.equal(t.wantsGauges('s1'), false)
    t.sessionStart('s1', 'compact')
    t.promptSubmit(cfg, 2000, 's1', 'again', '/repo')
    assert.equal(t.wantsGauges('s1'), true)
    t.promptSubmit(cfg, 3000, 's2', 'other', '/repo')
    t.measure('s2', gaugesFromUsage(reading))
    assert.equal(t.wantsGauges('s2'), false)
  })
  test('a phase-1 snapshot revives with no gauges', () => {
    const snap = parseSnapshot({ v: 1, sessions: [['s1', { state: 'running', message: 'go', changedAt: 1, hookAt: 1 }]], strays: [] })
    assert.ok(snap)
    const t = new Tracker()
    t.restore(snap.sessions)
    assert.deepEqual(gauges(t.tick(config(), 2)), [[undefined, undefined, undefined]])
  })
  test('a snapshot keeps the gauges across a reload', () => {
    const cfg = config()
    const t = new Tracker()
    t.promptSubmit(cfg, 1000, 's1', 'go', '/repo')
    t.measure('s1', gaugesFromUsage(reading))
    const snap = parseSnapshot(JSON.parse(JSON.stringify({ v: 1, sessions: t.entries(), strays: [] })))
    assert.ok(snap)
    const next = new Tracker()
    next.restore(snap.sessions)
    assert.deepEqual(gauges(next.tick(cfg, 2000)), [[30, 12, 1778614633]])
  })
})

describe('snapshot for a hot reload', () => {
  const reborn = (t: Tracker): Tracker => {
    const snap = parseSnapshot(JSON.parse(JSON.stringify({ v: 1, sessions: t.entries(), strays: [], permissionMode: '', t3: '' })))
    assert.ok(snap)
    const next = new Tracker()
    next.restore(snap.sessions)
    return next
  }
  for (const sc of transitions.scenarios) {
    test(`${sc.name}: a reload between every step changes nothing`, () => {
      const env = parseEnvFile(transitions.env)
      const r = resolveConfig({ env: { ...env, EMBER_MOD_SOURCE: env.EMBER_SOURCE ?? '', EMBER_SERVER_URL: 'http://h' }, home: '/home/nobody' })
      assert.ok('config' in r)
      let t = new Tracker()
      sc.steps.forEach((step: Step, n: number) => {
        assert.deepEqual(drive(t, r.config, step), step.want, `step ${n} (${step.event} at ${step.at}s)`)
        t = reborn(t)
      })
    })
  }
  test('restore keeps what the new module already tracked', () => {
    const cfg = config()
    const old = new Tracker()
    old.promptSubmit(cfg, 1000, 's1', 'old', '/repo')
    const fresh = new Tracker()
    fresh.promptSubmit(cfg, 2000, 's1', 'new', '/repo')
    fresh.restore(old.entries())
    assert.equal(fresh.sessions.get('s1')?.message, 'new')
  })
  test('parseSnapshot drops junk and fills missing fields', () => {
    assert.equal(parseSnapshot(undefined), undefined)
    assert.equal(parseSnapshot({ v: 2, sessions: [] }), undefined)
    const snap = parseSnapshot({
      v: 1,
      sessions: [['ok', { state: 'running', message: 7, changedAt: 5 }], ['no-state', {}], [3, {}], 'bad'],
      strays: [['s', [1, 'x', 2]], ['t', 'bad']],
      permissionMode: 4,
      t3: 'maybe',
    })
    assert.ok(snap)
    assert.deepEqual(snap.sessions.map(([id]) => id), ['ok'])
    assert.equal(snap.sessions[0]?.[1].message, '')
    assert.equal(snap.sessions[0]?.[1].changedAt, 5)
    assert.deepEqual(snap.strays, [['s', [1, 2]]])
    assert.equal(snap.permissionMode, '')
    assert.equal(snap.t3, '')
  })
  test('stray POST bookkeeping survives a reload only while it can still matter', () => {
    const sender = new Sender()
    const now = 1_000_000
    sender.restore(
      [
        ['young', [now - 1000]],
        ['old', [now - STRAY_SKIP_MS - 1]],
      ],
      now,
    )
    assert.deepEqual(sender.entries(), [['young', [now - 1000]]])
    assert.equal(sender.lateSends(), 1)
  })
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

describe('bounded', () => {
  test('a send that never settles gives up when the timer fires and frees its lane', async () => {
    const box = new Outbox()
    let fire: () => void = () => {}
    const timer = new Promise<void>(resolve => (fire = resolve))
    const results: boolean[] = []
    void box.push('s1', async () => void results.push(await bounded(new Promise<boolean>(() => {}), timer, false)))
    let next = false
    const later = box.push('s1', async () => void (next = true))
    fire()
    await later
    assert.deepEqual(results, [false])
    assert.equal(next, true)
  })
  test('a timer that rejects never cuts a send short', async () => {
    let finish: (v: boolean) => void = () => {}
    const work = new Promise<boolean>(resolve => (finish = resolve))
    const out = bounded(work, Promise.reject(new Error('aborted')), false)
    finish(true)
    assert.equal(await out, true)
  })
})

describe('Sender', () => {
  const wait = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))
  const KEY = { source: 'mbp-mod', tool: 'claude' as const, session: 's1' }
  const post = (state: string): Effect => ({ method: 'POST', body: { ...KEY, state, source_card: true, session_bar: true } })
  const del: Effect = { method: 'DELETE', body: KEY }

  function server(slowFirstPost: number, answer = true) {
    const rows = new Map<string, string>()
    const log: string[] = []
    let first = true
    const deliver = async (e: Effect) => {
      const slow = first && e.method === 'POST'
      first = false
      if (slow) await wait(slowFirstPost)
      if (e.method === 'POST') rows.set(e.body.session, e.body.state)
      else rows.delete(e.body.session)
      log.push(e.method + (e.method === 'POST' ? ` ${e.body.state}` : '') + (slow ? ' (late)' : ''))
      return slow ? answer : true
    }
    return { rows, log, deliver }
  }

  test('a timed-out POST that lands after the DELETE is deleted again (reviewer harness)', async () => {
    const srv = server(300)
    let current: Effect = post('running')
    const io = { deliver: srv.deliver, timer: wait, reconcile: () => current }
    const sender = new Sender()
    void sender.push(post('running'), 50, io, 0)
    await wait(10)
    current = del
    void sender.push(del, 50, io, 10)
    await wait(500)
    assert.equal(srv.log.join(' -> '), 'DELETE -> POST running (late) -> DELETE')
    assert.equal(srv.rows.has('s1'), false)
    assert.equal(sender.outbox.busy, 0)
    assert.equal(sender.lateSends(), 0)
  })

  test('the repair runs even when the late POST reports failure (the server may have applied it)', async () => {
    const srv = server(300, false)
    let current: Effect = post('running')
    const io = { deliver: srv.deliver, timer: wait, reconcile: () => current }
    const sender = new Sender()
    void sender.push(post('running'), 50, io, 0)
    await wait(10)
    current = del
    void sender.push(del, 50, io, 10)
    await wait(500)
    assert.equal(srv.rows.has('s1'), false)
  })

  test('a late running that lands after done re-sends the current state', async () => {
    const srv = server(300)
    const io = { deliver: srv.deliver, timer: wait, reconcile: () => post('done') }
    const sender = new Sender()
    void sender.push(post('running'), 50, io, 0)
    await wait(10)
    void sender.push(post('done'), 50, io, 10)
    await wait(500)
    assert.equal(srv.log.join(' -> '), 'POST done -> POST running (late) -> POST done')
    assert.equal(srv.rows.get('s1'), 'done')
  })

  test('a repair send is not watched again, so a slow server cannot loop it', async () => {
    let calls = 0
    const io = {
      deliver: async () => {
        calls++
        await wait(100)
        return true
      },
      timer: wait,
      reconcile: () => post('running'),
    }
    const sender = new Sender()
    void sender.push(post('running'), 10, io, 0)
    await wait(400)
    assert.equal(calls, 2)
    assert.equal(sender.lateSends(), 0)
  })

  function manual() {
    const calls: { effect: Effect; land: (ok?: boolean) => void; expire: () => void }[] = []
    const timers: (() => void)[] = []
    const rows = new Map<string, string>()
    const log: string[] = []
    const io = {
      deliver: (effect: Effect) =>
        new Promise<boolean>(resolve => {
          calls.push({
            effect,
            land: (ok = true) => {
              if (effect.method === 'POST') rows.set(effect.body.session, effect.body.state)
              else rows.delete(effect.body.session)
              log.push(effect.method === 'POST' ? `POST ${effect.body.state}` : 'DELETE')
              resolve(ok)
            },
            expire: () => timers[calls.length - 1]?.(),
          })
        }),
      timer: () => new Promise<void>(resolve => timers.push(resolve)),
      reconcile: (): Effect => current,
    }
    let current: Effect = post('done')
    return { calls, timers, rows, log, io, setCurrent: (e: Effect) => void (current = e) }
  }
  const flush = () => new Promise<void>(resolve => setImmediate(resolve))

  test('a repair POST that lands after the DELETE is deleted again', async () => {
    const m = manual()
    const sender = new Sender()
    void sender.push(post('running'), 20, m.io, 0)
    await flush()
    m.timers[0]()
    await flush()
    m.calls[0].land()
    await flush()
    assert.equal(m.calls[1].effect.method, 'POST')
    m.timers[1]()
    await flush()
    m.setCurrent(del)
    void sender.push(del, 20, m.io, 1)
    await flush()
    m.calls[2].land()
    m.timers[2]()
    await flush()
    m.calls[1].land()
    await flush()
    assert.equal(m.calls[3].effect.method, 'DELETE')
    m.calls[3].land()
    m.timers[3]()
    await flush()
    assert.equal(m.log.join(' -> '), 'POST running -> DELETE -> POST done -> DELETE')
    assert.equal(m.rows.has('s1'), false)
    assert.equal(sender.lateSends(), 0)
  })

  test('a slow repair POST that lands over a newer state sends that state once more', async () => {
    const m = manual()
    const sender = new Sender()
    void sender.push(post('running'), 20, m.io, 0)
    await flush()
    m.timers[0]()
    await flush()
    m.calls[0].land()
    await flush()
    m.timers[1]()
    await flush()
    m.setCurrent(post('running'))
    m.calls[1].land()
    await flush()
    assert.equal(m.calls[2].effect.method === 'POST' && m.calls[2].effect.body.state, 'running')
    m.calls[2].land()
    m.timers[2]()
    await flush()
    assert.equal(m.calls.length, 3)
    assert.equal(m.rows.get('s1'), 'running')
    assert.equal(sender.lateSends(), 0)
  })

  test('a stray settling after the done TTL deletes the row instead of re-posting done', () => {
    const t = new Tracker()
    const cfg = config()
    t.promptSubmit(cfg, 0, 's1', 'x', HOME)
    t.stop(cfg, 1000, 's1', '', HOME)
    assert.equal(t.reconcile(cfg, 2000, 's1').method, 'POST')
    assert.equal(t.reconcile(cfg, 1000 + 30_000, 's1').method, 'DELETE')
  })

  test('heartbeat POSTs wait for a stray younger than 60 s, then go out; DELETEs never wait', async () => {
    const delivered: string[] = []
    let first = true
    const io = {
      deliver: (e: Effect) => {
        delivered.push(e.method)
        if (first) {
          first = false
          return new Promise<boolean>(() => {})
        }
        return Promise.resolve(true)
      },
      timer: wait,
      reconcile: () => post('running'),
    }
    const sender = new Sender()
    await sender.push(post('running'), 10, io, 0)
    assert.equal(sender.lateSends(), 1)
    await sender.push(post('running'), 10, io, 10_000, true)
    await sender.push(post('running'), 10, io, 59_999, true)
    await sender.push(del, 10, io, 59_999, true)
    assert.deepEqual(delivered, ['POST', 'DELETE'])
    await sender.push(post('running'), 10, io, 60_000, true)
    assert.deepEqual(delivered, ['POST', 'DELETE', 'POST'])
  })

  test('each stray is counted, so one landing does not hide another', async () => {
    let release: () => void = () => {}
    const firstOut = new Promise<boolean>(resolve => (release = () => resolve(true)))
    let n = 0
    const io = {
      deliver: () => {
        n++
        return n === 1 ? firstOut : n === 2 ? new Promise<boolean>(() => {}) : Promise.resolve(true)
      },
      timer: wait,
      reconcile: () => post('running'),
    }
    const sender = new Sender()
    await sender.push(post('running'), 10, io, 0)
    await sender.push(post('running'), 10, io, 1)
    assert.equal(sender.lateSends(), 2)
    release()
    await wait(30)
    assert.equal(sender.lateSends(), 1)
  })
})

describe('the agents cross-check for a permission wait', () => {
  const cfg = config()
  const waiting = (): Tracker => {
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's', 'go', '/repo')
    t.permissionRequest(cfg, 100, 's', 'Bash', { command: 'make' }, '/repo')
    return t
  }
  const busy = new Map([['s', row('busy')]])

  test('the CLI version gate matches the Go one', () => {
    assert.equal(agentsCLIUsable('2.1.288 (Claude Code)'), true)
    assert.equal(agentsCLIUsable('2.1.287 (Claude Code)'), false)
    assert.equal(agentsCLIUsable('2.2.0'), true)
    assert.equal(agentsCLIUsable('1.99.99'), false)
    assert.equal(agentsCLIUsable('not a version'), false)
    assert.equal(agentsCLIUsable(''), false)
  })

  test('only a dialog wait wants the poll', () => {
    const t = new Tracker()
    t.promptSubmit(cfg, 0, 's', 'go', '/repo')
    assert.equal(t.needsWatch(), false)
    t.notification(cfg, 1, 's', 'agent_needs_input', 'needs input', '/repo')
    assert.equal(t.needsWatch(), false)
    t.permissionRequest(cfg, 2, 's', 'Bash', {}, '/repo')
    assert.equal(t.needsWatch(), true)
    t.toolFinished(cfg, 3, 's', 'Bash', '')
    assert.equal(t.needsWatch(), true)
    t.tick(cfg, 20_000)
    assert.equal(t.needsWatch(), false)
  })

  test('the first busy sighting only records, the second 1.5 s later corrects and posts running', () => {
    const t = waiting()
    assert.deepEqual(t.agentsCorrect(cfg, 2000, 2000, busy), [])
    assert.deepEqual(t.agentsCorrect(cfg, 3000, 3000, busy), [])
    assert.deepEqual(states(t.agentsCorrect(cfg, 3500, 3600, busy)), ['running:Bash'])
    assert.equal(t.needsWatch(), true)
    t.tick(cfg, 20_000)
    assert.equal(t.needsWatch(), false)
  })

  test('a row that goes back to waiting resets the count', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    t.agentsCorrect(cfg, 3000, 3000, new Map([['s', row('waiting')]]))
    assert.deepEqual(t.agentsCorrect(cfg, 4000, 4000, busy), [])
    assert.deepEqual(states(t.agentsCorrect(cfg, 5600, 5600, busy)), ['running:Bash'])
  })

  test('a state change between sightings starts the count again', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    t.permissionRequest(cfg, 2500, 's', 'Edit', {}, '/repo')
    assert.deepEqual(t.agentsCorrect(cfg, 4000, 4000, busy), [])
    assert.deepEqual(states(t.agentsCorrect(cfg, 6000, 6000, busy)), ['running:Edit'])
  })

  test('a row fetched before a newer event is ignored', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    t.agentsCorrect(cfg, 3000, 3000, busy)
    t.permissionRequest(cfg, 3200, 's', 'Edit', {}, '/repo')
    assert.deepEqual(t.agentsCorrect(cfg, 3100, 3300, busy), [])
    assert.equal(t.needsWatch(), true)
  })

  test('the late permission_prompt Notification after a correction is dropped as after a PostToolUse', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    t.agentsCorrect(cfg, 4000, 4000, busy)
    assert.deepEqual(t.notification(cfg, 6000, 's', 'permission_prompt', 'Claude needs your permission to use Bash', '/repo'), [])
  })

  test('a session the CLI does not list is left alone', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, new Map())
    assert.deepEqual(t.agentsCorrect(cfg, 4000, 4000, new Map()), [])
    assert.equal(t.needsWatch(), true)
  })

  test('a wrong flip is undone: a waiting row right after it restores the wait', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    assert.deepEqual(states(t.agentsCorrect(cfg, 4000, 4000, busy)), ['running:Bash'])
    const back = new Map([['s', row('waiting', 'permission prompt')]])
    assert.equal(t.needsWatch(), true)
    assert.deepEqual(t.agentsCorrect(cfg, 5000, 5000, back), [])
    assert.deepEqual(states(t.agentsCorrect(cfg, 6600, 6600, back)), ['waiting:permission prompt'])
    assert.equal(t.needsWatch(), true)
    assert.deepEqual(t.agentsCorrect(cfg, 7000, 7000, busy), [])
    assert.deepEqual(states(t.agentsCorrect(cfg, 8600, 8600, busy)), ['running:Bash'])
  })

  test('a waiting row is ignored after the 15 s window, or once any event followed the flip', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    t.agentsCorrect(cfg, 4000, 4000, busy)
    const back = new Map([['s', row('waiting')]])
    t.agentsCorrect(cfg, 20_000, 20_000, back)
    assert.deepEqual(t.agentsCorrect(cfg, 22_000, 22_000, back), [])
    assert.equal(t.needsWatch(), false)
    const u = waiting()
    u.agentsCorrect(cfg, 2000, 2000, busy)
    u.agentsCorrect(cfg, 4000, 4000, busy)
    u.preToolUse(cfg, 4500, 's', 'Read', {}, 'toolu_2', '/repo')
    u.agentsCorrect(cfg, 5000, 5000, back)
    assert.deepEqual(u.agentsCorrect(cfg, 7000, 7000, back), [])
  })

  test('a failed call drops the sightings, so a lone busy row later cannot flip at once', () => {
    const t = waiting()
    t.agentsCorrect(cfg, 2000, 2000, busy)
    assert.equal(t.confirmDue(3600), true)
    t.agentsFailed()
    assert.equal(t.confirmDue(3600), false)
    assert.deepEqual(t.agentsCorrect(cfg, 302_000, 302_000, busy), [])
    assert.equal(t.needsWatch(), true)
  })
})
