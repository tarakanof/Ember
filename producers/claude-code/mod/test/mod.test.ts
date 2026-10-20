import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/Users/tester'
const ENV_FILE = `${HOME}/.config/ember/producer.env`
const ENV_TEXT = 'EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://127.0.0.1:9\nEMBER_TOKEN=test-token\n'

type Sent = { url: string; method: string; auth: string; body: Record<string, unknown> }

type EnvFile = { text: string; mode: string; owner: string; isLink: boolean }

const TRUSTED: EnvFile = { text: ENV_TEXT, mode: '600', owner: '501', isLink: false }

type Gate = { wait: Promise<void>; open: () => void }

function gate(): Gate {
  let open: () => void = () => {}
  const wait = new Promise<void>(resolve => (open = resolve))
  return { wait, open }
}

function ran(stdout: string, exitCode = 0) {
  return { value: { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
}

type Proc = { ppid: number; comm: string; argv?: string[] }

const T3_DESKTOP = '/Applications/T3 Code (Alpha).app/Contents/MacOS/T3 Code (Alpha)'

function t3Tree(launcherArgv: string[]): Record<string, Proc> {
  return {
    '100': { ppid: 99, comm: '/bin/sh' },
    '99': { ppid: 98, comm: 'claude' },
    '98': { ppid: 1, comm: launcherArgv[0] ?? '', argv: launcherArgv },
  }
}

const T3_SERVER = [T3_DESKTOP, '--require', '/x/compileCache.cjs', '/Applications/T3 Code (Alpha).app/Contents/Resources/app.asar/apps/server/dist/bin.mjs', '--bootstrap-fd', '3']
const SDK_ENV = { CLAUDE_CODE_ENTRYPOINT: 'sdk-ts' }

function heldState(on: On, initial?: unknown) {
  const held = { value: initial as unknown, version: initial === undefined ? 0 : 1 }
  on('state.get', () => ({ value: { value: held.value, version: held.version } }))
  on('state.set', (_$, e) => {
    held.value = e.value
    held.version++
    return { value: { isSet: true as const, version: held.version } }
  })
  return held
}

function world(on: On, envFile: EnvFile | null = TRUSTED, extraEnv: Record<string, string> = {}) {
  const sent: Sent[] = []
  const done: string[] = []
  const ctl = {
    killSwitch: false,
    sessionId: 'sess-1',
    envRead: undefined as Gate | undefined,
    fetch: {} as Record<string, Gate | undefined>,
    envReads: 0,
    runs: [] as string[][],
    holdNext: undefined as Gate | undefined,
    statFails: false,
    absoluteMissing: false,
    hostName: '',
    hostRuns: 0,
    rejectProject: false,
    procs: {} as Record<string, Proc>,
    psCalls: 0,
    claudeVersion: '2.1.305 (Claude Code)\n',
    agents: '[]',
    agentsFail: false,
    agentsRuns: 0,
  }
  mock.env(on, { HOME, ...extraEnv })
  const clock = mock.clock(on, { now: 1_000_000 })
  on('fs.read', async ($, e) => {
    if (e.path !== ENV_FILE || envFile === null) return { deny: 'ENOENT' }
    ctl.envReads++
    if (ctl.envRead) await ctl.envRead.wait
    return { value: envFile.text }
  })
  on('fs.stat', ($, e) =>
    e.path === ENV_FILE && envFile !== null
      ? { value: { kind: 'file' as const, size: envFile.text.length, mtimeMs: 0, isLink: envFile.isLink } }
      : { deny: 'ENOENT' },
  )
  on('process.run', ($, e) => {
    ctl.runs.push([...e.argv])
    const bin = e.argv[0] ?? ''
    if (/(^|\/)(scutil|hostname)$/.test(bin)) {
      ctl.hostRuns++
      if (bin.endsWith('scutil') && ctl.hostName !== '') return ran(ctl.hostName + '\n')
      return ran('', 127)
    }
    if (/(^|\/)sh$/.test(bin) && e.argv[1] === '-c') {
      if (e.argv[2] === 'echo $PPID') return ran('100\n')
      return ran('', 1)
    }
    if (/(^|\/)ps$/.test(bin)) {
      ctl.psCalls++
      const proc = ctl.procs[e.argv[e.argv.length - 1] ?? '']
      if (!proc) return ran('', 1)
      if (e.argv.includes('ppid=,comm=')) return ran(`${proc.ppid} ${proc.comm}\n`)
      if (e.argv.includes('comm=')) return ran(proc.comm + '\n')
      if (e.argv.includes('args=')) return ran((proc.argv ?? [proc.comm]).join(' ') + '\n')
    }
    if (/(^|\/)claude$/.test(bin)) {
      if (e.argv[1] === '--version') return ran(ctl.claudeVersion)
      if (e.argv[1] === 'agents') {
        ctl.agentsRuns++
        return ctl.agentsFail ? ran('', 1) : ran(ctl.agents)
      }
    }
    const usable = ctl.absoluteMissing ? !bin.startsWith('/') : bin.startsWith('/usr/bin/')
    if (!usable) return ran('', 127)
    if (bin.endsWith('id')) return ran('501\n')
    if (bin.endsWith('stat') && e.argv[1] === '-f' && envFile !== null && !ctl.statFails) return ran(`${envFile.mode} ${envFile.owner}\n`)
    return ran('', 1)
  })
  on('fs.exists', ($, e) => ({ value: e.path.endsWith('/claude-hooks.disabled') && ctl.killSwitch }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('classic.*', () => ({}))
  on('session.id', () => ({ value: ctl.sessionId }))
  on('session.cwd', () => ({ value: `${HOME}/proj` }))
  on('http.fetch', async ($, e) => {
    const method = e.init?.method ?? 'GET'
    sent.push({
      url: e.url,
      method,
      auth: e.init?.headers?.Authorization ?? '',
      body: JSON.parse(e.init?.body ?? '{}') as Record<string, unknown>,
    })
    if (ctl.rejectProject && e.init?.method === 'POST' && 'project' in JSON.parse(e.init?.body ?? '{}')) {
      return { value: { status: 400, ok: false, headers: {}, text: '{"error":"json: unknown field \\"project\\""}' } }
    }
    const once = ctl.holdNext
    ctl.holdNext = undefined
    if (once) await once.wait
    const held = ctl.fetch[method]
    if (held) await held.wait
    done.push(method)
    return { value: { status: 200, ok: true, headers: {}, text: '{"ok":true}' } }
  })
  return { sent, done, clock, ctl }
}

const START = { cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true } as const
const PROMPT = { prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` }
const STOP = { stop_hook_active: false, last_assistant_message: 'done', session_id: 'sess-1', cwd: `${HOME}/proj` }
const END = { reason: 'prompt_input_exit', session_id: 'sess-1', cwd: `${HOME}/proj` } as const

test('a prompt posts running under the mod source with the bearer token', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'fix the build', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  expect(sent).toHaveLength(1)
  expect(sent[0]).toEqual({
    url: 'http://127.0.0.1:9/v1/status',
    method: 'POST',
    auth: 'Bearer test-token',
    body: {
      source: 'mbp-mod',
      tool: 'claude',
      session: 'sess-1',
      state: 'running',
      message: 'fix the build',
      source_card: true,
      session_bar: true,
      project: 'proj',
    },
  })
})

test('a placeholder EMBER_SOURCE posts under the host id', async ($, on) => {
  const placeholder = { ...TRUSTED, text: 'EMBER_SOURCE=set-me-to-this-laptop-id\nEMBER_SERVER_URL=http://127.0.0.1:9\n' }
  const { sent, clock, ctl } = world(on, placeholder)
  ctl.hostName = 'Dmitrys-MacBook-Pro'
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  expect(sent.map(s => s.body.source)).toEqual(['mbp-mod'])
})

test('a host that resolves to nothing is looked up once, then again after 10 minutes', async ($, on) => {
  const placeholder = { ...TRUSTED, text: 'EMBER_SOURCE=\nEMBER_SERVER_URL=http://127.0.0.1:9\n' }
  const { sent, clock, ctl } = world(on, placeholder)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  const first = ctl.hostRuns
  expect(first).toBeGreaterThan(0)
  await clock.advance(10_000)
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(ctl.hostRuns).toBe(first)
  await clock.advance(600_000)
  expect(ctl.hostRuns).toBeGreaterThan(first)
  expect(sent).toHaveLength(0)
})

test('a server that rejects project gets the POST again without it', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.rejectProject = true
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  expect(sent.map(s => 'project' in s.body)).toEqual([true, false])
})

test('the heartbeat re-posts a running session every 10 s', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'running', 'running'])
})

test('SessionEnd deletes the session', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await $.classic.SessionEnd({ reason: 'prompt_input_exit', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  expect(sent.at(-1)).toEqual({
    url: 'http://127.0.0.1:9/v1/status',
    method: 'DELETE',
    auth: 'Bearer test-token',
    body: { source: 'mbp-mod', tool: 'claude', session: 'sess-1' },
  })
})

test('a tool.check ask shows waiting in an interactive session', async ($, on) => {
  const { sent, clock } = world(on)
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  const verdict = await $.tool.check({ tool: 'Bash', input: { command: 'rm -rf build' }, tool_use_id: 'toolu_1' })
  await clock.settle()
  expect(verdict.decision).toBe('ask')
  expect(sent).toHaveLength(2)
  expect(sent[1]?.body).toEqual(
    expect.objectContaining({
      session: 'sess-1',
      state: 'waiting',
      message: 'approve Bash',
      activity: 'Bash: rm -rf build',
    }),
  )
})

test('the asked call finishing ends the wait', async ($, on) => {
  const { sent, clock } = world(on)
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  let started: (id: string) => void = () => {}
  const callId = new Promise<string>(resolve => (started = resolve))
  let approve: () => void = () => {}
  const approved = new Promise<void>(resolve => (approve = resolve))
  on('tool.call', async ($, e) => {
    started(e.tool_use_id)
    await approved
    return { result: { stdout: '', stderr: '', interrupted: false } }
  })
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  const call = $.tool.call({ tool: 'Bash', command: 'make', description: 'build' })
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: await callId })
  await clock.settle()
  expect(sent.at(-1)?.body.state).toBe('waiting')
  approve()
  await call
  await clock.settle()
  expect(sent.map(s => [s.body.state, s.body.message])).toEqual([
    ['running', 'go'],
    ['running', 'Bash'],
    ['waiting', 'approve Bash'],
    ['running', 'Bash'],
  ])
})

test('a headless session never shows waiting from tool.check', async ($, on) => {
  const { sent, clock } = world(on)
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start({ cwd: `${HOME}/proj`, surface: null, isInteractive: false })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: 'toolu_1' })
  await clock.settle()
  expect(sent.map(s => s.body.state)).toEqual(['running'])
})

test('no producer.env keeps the mod silent', async ($, on) => {
  const { sent, clock } = world(on, null)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.advance(10_000)
  expect(sent).toEqual([])
})

for (const [name, envFile] of [
  ['world-readable', { ...TRUSTED, mode: '644' }],
  ['owned by another user', { ...TRUSTED, owner: '0' }],
  ['a symlink', { ...TRUSTED, isLink: true }],
] as const) {
  test(`a producer.env that is ${name} keeps the mod silent, as the Go reader refuses it`, async ($, on) => {
    const { sent, clock } = world(on, envFile)
    await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
    await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
    await clock.advance(10_000)
    expect(sent).toEqual([])
  })
}

test('the kill switch silences the very next event, as it does each Go hook', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  ctl.killSwitch = true
  await $.classic.Stop({ stop_hook_active: false, last_assistant_message: 'done', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await $.classic.SessionEnd({ reason: 'prompt_input_exit', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running'])
})

test('an event during a heartbeat reload still lands (the reload swaps the config in one step)', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  ctl.envRead = gate()
  await clock.advance(10_000)
  await $.classic.Stop(STOP)
  await clock.settle()
  ctl.envRead.open()
  await clock.settle()
  expect(sent.map(s => s.body.state)).toContain('done')
  await clock.advance(10_000)
  expect(sent.at(-1)?.body.state).toBe('done')
})

test('SessionEnd gives up on a stalled DELETE after the 500 ms hook timeout, inside its 800 ms cap', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  ctl.fetch.DELETE = gate()
  let ended = false
  const end = $.classic.SessionEnd(END).then(() => (ended = true))
  await clock.advance(499)
  expect(ended).toBe(false)
  await clock.advance(1)
  await end
  expect(ended).toBe(true)
  expect(sent.at(-1)?.method).toBe('DELETE')
  ctl.fetch.DELETE.open()
})

test('a DELETE waits for a POST still inside its timeout for the same session', async ($, on) => {
  const { done, clock, ctl } = world(on)
  await $.session.start(START)
  ctl.fetch.POST = gate()
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const end = $.classic.SessionEnd(END)
  await clock.settle()
  ctl.fetch.POST.open()
  await clock.advance(800)
  await end
  await clock.settle()
  expect(done).toEqual(['POST', 'DELETE'])
})

test('a heartbeat is skipped while the previous one is still reloading', async ($, on) => {
  const { clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const before = ctl.envReads
  ctl.envRead = gate()
  await clock.advance(10_000)
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(ctl.envReads).toBe(before + 1)
  ctl.envRead.open()
})

test('tool hooks hash an invalid session id with the cwd, as classic hooks do', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.sessionId = 'not/a/valid/id'
  on('tool.call', () => ({ result: { stdout: '', stderr: '', interrupted: false } }))
  await $.session.start(START)
  await $.classic.UserPromptSubmit({ ...PROMPT, session_id: 'not/a/valid/id' })
  await $.tool.call({ tool: 'Bash', command: 'ls', description: 'list' })
  await clock.settle()
  expect(sent).toHaveLength(2)
  expect(sent[1]?.body.session).toBe(sent[0]?.body.session)
  expect(sent[0]?.body.session).not.toBe('not/a/valid/id')
})

test('id -u runs once, by absolute path, and only stat runs on later reloads', async ($, on) => {
  const { clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(ctl.runs.filter(argv => argv[0] === '/usr/bin/id')).toHaveLength(1)
  expect(ctl.runs.every(argv => argv[0]?.startsWith('/usr/bin/'))).toBe(true)
})

test('with the kill switch present nothing is spawned', async ($, on) => {
  const { clock, ctl } = world(on)
  ctl.killSwitch = true
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.advance(10_000)
  expect(ctl.runs).toEqual([])
})

test('a session with no session.start still loads and heartbeats on its first event', async ($, on) => {
  const { sent, clock } = world(on)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'running'])
})

test('a hung send releases its session after the hook timeout, so later sends still go out', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await clock.settle()
  ctl.holdNext = gate()
  await $.classic.UserPromptSubmit(PROMPT)
  await $.classic.Stop(STOP)
  await clock.settle()
  expect(sent.map(s => s.body.state)).toEqual(['running'])
  await clock.advance(500)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'done'])
  const end = $.classic.SessionEnd(END)
  await clock.advance(800)
  await end
  expect(sent.at(-1)?.method).toBe('DELETE')
})

test('a stat that gives no answer keeps the previous config instead of silencing the mod', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  ctl.statFails = true
  await clock.advance(10_000)
  await $.classic.Stop(STOP)
  await clock.settle()
  expect(sent.at(-1)?.body.state).toBe('done')
})

test('stat and id fall back to a PATH lookup when the absolute paths are missing', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.absoluteMissing = true
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.map(s => s.body.state)).toEqual(['running'])
  expect(ctl.runs.some(argv => argv[0] === 'stat')).toBe(true)
})

test('a timed-out POST that lands after the SessionEnd DELETE is deleted again', async ($, on) => {
  const { done, clock, ctl } = world(on)
  await $.session.start(START)
  await clock.settle()
  const slow = gate()
  ctl.holdNext = slow
  await $.classic.UserPromptSubmit(PROMPT)
  const end = $.classic.SessionEnd(END)
  await clock.advance(800)
  await end
  expect(done).toEqual(['DELETE'])
  slow.open()
  await clock.settle()
  await clock.advance(500)
  const rows = new Set<string>()
  for (const method of done) method === 'POST' ? rows.add('sess-1') : rows.delete('sess-1')
  expect(done).toEqual(['DELETE', 'POST', 'DELETE'])
  expect(rows.size).toBe(0)
})

test('heartbeats keep running on the kept config while the stat check cannot run', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  ctl.statFails = true
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'running', 'running'])
})

test('heartbeats skip a session only while its stray POST is younger than 60 s', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await clock.settle()
  ctl.holdNext = gate()
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.advance(500)
  await clock.advance(49_500)
  expect(sent).toHaveLength(1)
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'running'])
})

test('a heartbeat re-posts a session the previous copy of the module tracked, restored from $.state', async ($, on) => {
  const held = heldState(on)
  const { sent, clock } = world(on)
  const at = clock.now()
  const seeded = {
    v: 1 as const,
    sessions: [
      [
        'sess-1',
        {
          state: 'running',
          message: 'before reload',
          activity: 'Bash: make',
          project: 'proj',
          changedAt: at - 5000,
          hookAt: at - 5000,
          pendingPermission: false,
          pendingTool: '',
          pendingToolUseId: '',
          askedTool: '',
          askedToolUseId: '',
          lastTool: 'Bash',
          lastToolUseId: 'toolu_1',
          resumedTool: '',
          resumedAt: 0,
          agentInput: false,
          parked: false,
        },
      ],
    ],
    strays: [],
    permissionMode: '',
    t3: '' as const,
  }
  held.value = seeded
  held.version = 1
  await $.session.start(START)
  await clock.advance(10_000)
  expect(sent.map(s => [s.method, s.body.session, s.body.state, s.body.message, s.body.activity])).toEqual([
    ['POST', 'sess-1', 'running', 'before reload', 'Bash: make'],
  ])
})

test('the tracker is written to $.state after each event so a reload can read it back', async ($, on) => {
  const held = heldState(on)
  const { clock } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const sessions = (held.value as { sessions: [string, { state: string; message: string }][] } | undefined)?.sessions ?? []
  expect(sessions.map(([id, s]) => [id, s.state, s.message])).toEqual([['sess-1', 'running', 'go']])
  await $.classic.SessionEnd(END)
  await clock.settle()
  expect((held.value as { sessions: unknown[] }).sessions).toEqual([])
})

test('a T3-spawned session posts nothing and sends no DELETE', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, SDK_ENV)
  ctl.procs = t3Tree(T3_SERVER)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.classic.Stop(STOP)
  await $.classic.SessionEnd(END)
  await clock.advance(10_000)
  expect(sent).toEqual([])
  expect(ctl.psCalls).toBeGreaterThan(0)
})

test('the T3 verdict is taken once per process, not on every event', async ($, on) => {
  const { clock, ctl } = world(on, TRUSTED, SDK_ENV)
  ctl.procs = t3Tree(T3_SERVER)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  const first = ctl.psCalls
  await $.classic.Stop(STOP)
  await clock.advance(10_000)
  expect(ctl.psCalls).toBe(first)
})

test('an SDK session that no T3 server launched still posts', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, SDK_ENV)
  ctl.procs = t3Tree(['node', '/Users/u/agent/run.mjs'])
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.map(s => s.body.state)).toEqual(['running'])
})

test('an interactive session never looks at the process tree', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, { CLAUDE_CODE_ENTRYPOINT: 'cli' })
  ctl.procs = t3Tree(T3_SERVER)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(ctl.psCalls).toBe(0)
  expect(sent.map(s => s.body.state)).toEqual(['running'])
})

test('a failed process lookup reports the session, and a later one that finds T3 deletes the row', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, SDK_ENV)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.map(s => s.body.state)).toEqual(['running'])
  ctl.procs = t3Tree(T3_SERVER)
  await $.classic.Stop(STOP)
  await clock.settle()
  expect(sent.map(s => s.method)).toEqual(['POST', 'POST'])
  await clock.advance(30_000)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.at(-1)).toEqual(expect.objectContaining({ method: 'DELETE', body: { source: 'mbp-mod', tool: 'claude', session: 'sess-1' } }))
  const count = sent.length
  await clock.advance(10_000)
  await $.classic.Stop(STOP)
  await clock.settle()
  expect(sent).toHaveLength(count)
})

function agentRows(status: string, sessionId = 'sess-1'): string {
  return JSON.stringify([{ pid: 7, kind: 'interactive', sessionId, cwd: `${HOME}/proj`, status }])
}

async function openDialog($: Parameters<Parameters<typeof test>[1]>[0], on: On, clock: ReturnType<typeof world>['clock']) {
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: 'toolu_1' })
  await clock.settle()
}

test('a busy row ends a permission wait before the approved tool finishes', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await openDialog($, on, clock)
  expect(sent.at(-1)?.body.state).toBe('waiting')
  await clock.advance(2000)
  expect(sent.at(-1)?.body.state).toBe('waiting')
  await clock.advance(2000)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Bash' }))
  expect(ctl.runs.filter(argv => argv[1] === 'agents' && argv[2] === '--json')).toHaveLength(2)
})

test('a waiting row keeps the wait, and the poll goes on', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('waiting')
  await openDialog($, on, clock)
  await clock.advance(2000)
  await clock.advance(2000)
  await clock.advance(2000)
  expect(sent.map(s => s.body.state)).toEqual(['running', 'waiting'])
  expect(ctl.agentsRuns).toBe(3)
})

test('a busy row seen once is not enough: it must hold for 1.5 s', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await openDialog($, on, clock)
  await clock.advance(2000)
  ctl.agents = agentRows('waiting')
  await clock.advance(2000)
  expect(sent.at(-1)?.body.state).toBe('waiting')
})

test('the poll stops once the wait ends and never runs without a dialog wait', async ($, on) => {
  const { clock, ctl } = world(on)
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.advance(8000)
  expect(ctl.agentsRuns).toBe(0)
  expect(ctl.runs.some(argv => /claude$/.test(argv[0] ?? ''))).toBe(false)
  ctl.agents = agentRows('busy')
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: 'toolu_1' })
  await clock.advance(2000)
  await clock.advance(2000)
  const runs = ctl.agentsRuns
  expect(runs).toBe(2)
  await clock.advance(6000)
  expect(ctl.agentsRuns).toBe(runs)
})

test('a classic PermissionRequest wait is ended the same way', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.classic.PermissionRequest({ tool_name: 'Edit', tool_input: { file_path: '/x' }, session_id: 'sess-1', cwd: `${HOME}/proj`, permission_mode: 'auto' })
  await clock.settle()
  expect(sent.at(-1)?.body.state).toBe('waiting')
  await clock.advance(2000)
  await clock.advance(2000)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Edit' }))
})

test('a CLI older than 2.1.288 is never asked for agents, and a failing call backs off for 5 minutes', async ($, on) => {
  const { clock, ctl } = world(on)
  ctl.claudeVersion = '2.1.287 (Claude Code)\n'
  await openDialog($, on, clock)
  await clock.advance(2000)
  await clock.advance(2000)
  expect(ctl.runs.some(argv => argv[1] === 'agents')).toBe(false)
  ctl.claudeVersion = '2.1.305 (Claude Code)\n'
  ctl.agentsFail = true
  await clock.advance(300_000)
  const failed = ctl.agentsRuns
  expect(failed).toBeGreaterThan(0)
  await clock.advance(2000)
  await clock.advance(2000)
  expect(ctl.agentsRuns).toBe(failed)
})

test('a busy row must be seen twice on the same wait: a new ask in between starts the count again', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await openDialog($, on, clock)
  await clock.advance(2000)
  await $.classic.PermissionRequest({ tool_name: 'Edit', tool_input: { file_path: '/x' }, session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.advance(2000)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'waiting', message: 'approve Edit' }))
  await clock.advance(2000)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Edit' }))
})
