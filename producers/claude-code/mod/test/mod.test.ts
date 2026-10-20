import { expect, mock, test } from 'claude-code/testing'
import type { On, SessionUsage } from 'claude-code'

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

const statTail = (ppid: number) => `S ${ppid} 98 98 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 5000 1000000 500`

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
    sessionFiles: 1,
    listCalls: 0,
    usage: { startedAt: 0, context: { window: 200000 }, rateLimits: [] } as SessionUsage,
    usageCalls: 0,
    usageHold: undefined as Gate | undefined,
    sessionIdFails: false,
    psHold: undefined as Gate | undefined,
    proc: undefined as Record<string, { stat: string; cmdline: string }> | undefined,
  }
  mock.env(on, { HOME, ...extraEnv })
  const clock = mock.clock(on, { now: 1_000_000 })
  on('fs.read', async ($, e) => {
    const procPath = /^\/proc\/(\d+)\/(stat|cmdline)$/.exec(e.path)
    if (procPath) {
      const entry = ctl.proc?.[procPath[1] ?? '']
      return entry ? { value: procPath[2] === 'stat' ? entry.stat : entry.cmdline } : { deny: 'ENOENT' }
    }
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
  on('process.run', async ($, e) => {
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
      if (ctl.psHold) await ctl.psHold.wait
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
  on('fs.list', ($, e) => {
    if (!e.path.endsWith('/.claude/sessions')) return { deny: 'ENOENT' }
    ctl.listCalls++
    return {
      value: [{ name: '7.json', kind: 'file' as const, size: 40, mtimeMs: ctl.sessionFiles, isLink: false }],
    }
  })
  on('fs.exists', ($, e) => ({
    value: e.path.endsWith('/claude-hooks.disabled') ? ctl.killSwitch : e.path === '/proc/self/stat' && ctl.proc !== undefined,
  }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('classic.*', () => ({}))
  on('session.id', () => {
    if (ctl.sessionIdFails) throw new Error('no session')
    return { value: ctl.sessionId }
  })
  on('session.measure', (_$, e) => ({ changed: e.changed }))
  on('session.cwd', () => ({ value: `${HOME}/proj` }))
  on('session.usage', async () => {
    ctl.usageCalls++
    const answer = ctl.usage
    if (ctl.usageHold) await ctl.usageHold.wait
    return { value: answer }
  })
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

test('a host found on the 10-minute retry is used by the next hook', async ($, on) => {
  const placeholder = { ...TRUSTED, text: 'EMBER_SOURCE=\nEMBER_SERVER_URL=http://127.0.0.1:9\n' }
  const { sent, clock, ctl } = world(on, placeholder)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent).toHaveLength(0)
  ctl.hostName = 'Dmitrys-MacBook-Pro'
  await clock.advance(600_000)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.map(s => s.body.source)).toEqual(['mbp-mod'])
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

test('an event that arrives while the T3 lookup runs waits for its verdict and posts nothing when it is T3', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, SDK_ENV)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent.map(s => s.method)).toEqual(['POST'])
  ctl.procs = t3Tree(T3_SERVER)
  await clock.advance(30_000)
  const held = gate()
  ctl.psHold = held
  const posted = sent.length
  const first = $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const second = $.classic.Stop(STOP)
  await clock.settle()
  expect(sent).toHaveLength(posted)
  held.open()
  await Promise.all([first, second])
  await clock.settle()
  expect(sent.slice(posted).map(s => s.method)).toEqual(['DELETE'])
})

test('on Linux the T3 walk reads /proc through the file API and spawns no ps', async ($, on) => {
  const { sent, clock, ctl } = world(on, TRUSTED, SDK_ENV)
  ctl.proc = {
    '100': { stat: `100 (sh) ${statTail(99)}`, cmdline: 'sh\0-c\0echo $PPID\0' },
    '99': { stat: `99 (claude) ${statTail(98)}`, cmdline: 'claude\0' },
    '98': { stat: `98 (T3 Code (Alpha)) ${statTail(1)}`, cmdline: T3_SERVER.join('\0') + '\0' },
  }
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(sent).toEqual([])
  expect(ctl.psCalls).toBe(0)
  expect(ctl.runs.some(argv => argv.includes('-c') && argv.some(a => a.includes('/proc')))).toBe(false)
})

test('on macOS the T3 probe never tries /proc through a shell', async ($, on) => {
  const { clock, ctl } = world(on, TRUSTED, SDK_ENV)
  ctl.procs = t3Tree(['node', '/Users/u/agent/run.mjs'])
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(ctl.runs.filter(argv => argv.some(a => a.includes('/proc')))).toEqual([])
})

test('heartbeats with nothing changed do not rewrite the $.state snapshot', async ($, on) => {
  const held = heldState(on)
  const { clock } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const written = held.version
  expect(written).toBeGreaterThan(0)
  await clock.advance(10_000)
  await clock.advance(10_000)
  await clock.advance(10_000)
  expect(held.version).toBe(written)
})

test('session.start answers without waiting for the snapshot read', async ($, on) => {
  const read = gate()
  on('state.get', async () => {
    await read.wait
    return { value: { value: undefined, version: 0 } }
  })
  on('state.set', () => ({ value: { isSet: true as const, version: 1 } }))
  const { clock } = world(on)
  const started = $.session.start(START).then(() => 'started')
  const blocked = clock.sleep(100).then(() => 'blocked')
  const first = Promise.race([started, blocked])
  await clock.advance(100)
  expect(await first).toBe('started')
  read.open()
  await clock.settle()
})

function agentRows(status: string, waitingFor = '', sessionId = 'sess-1'): string {
  return JSON.stringify([{ pid: 7, kind: 'interactive', sessionId, cwd: `${HOME}/proj`, status, waitingFor }])
}

type Clock = ReturnType<typeof world>['clock']

async function openDialog($: Parameters<Parameters<typeof test>[1]>[0], on: On, clock: Clock) {
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: 'toolu_1' })
  await clock.settle()
}

async function seconds($: unknown, clock: Clock, n: number) {
  for (let i = 0; i < n; i++) await clock.advance(1000)
}

const agentCalls = (ctl: { runs: string[][] }) => ctl.runs.filter(argv => argv[1] === 'agents').length

test('an answered dialog ends the wait: a session-file change triggers one call, a second confirms', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await openDialog($, on, clock)
  await seconds($, clock, 3)
  expect(agentCalls(ctl)).toBe(0)
  expect(sent.at(-1)?.body.state).toBe('waiting')
  ctl.agents = agentRows('busy')
  ctl.sessionFiles++
  await seconds($, clock, 1)
  expect(sent.at(-1)?.body.state).toBe('waiting')
  await seconds($, clock, 2)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Bash' }))
  expect(agentCalls(ctl)).toBe(2)
})

test('with no session-file change and no confirmation due, nothing is spawned until the 60 s fallback', async ($, on) => {
  const { clock, ctl } = world(on)
  ctl.agents = agentRows('waiting', 'permission prompt')
  await openDialog($, on, clock)
  await seconds($, clock, 50)
  expect(agentCalls(ctl)).toBe(0)
  await seconds($, clock, 15)
  expect(agentCalls(ctl)).toBe(1)
})

test('the poll never runs without a dialog wait and stops when it ends', async ($, on) => {
  const { clock, ctl } = world(on)
  on('tool.check', () => ({ decision: 'ask', reason: 'needs approval' }))
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await seconds($, clock, 8)
  expect(ctl.listCalls).toBe(0)
  expect(ctl.runs.some(argv => /claude$/.test(argv[0] ?? ''))).toBe(false)
  ctl.agents = agentRows('busy')
  await $.tool.check({ tool: 'Bash', input: { command: 'make' }, tool_use_id: 'toolu_1' })
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 3)
  await seconds($, clock, 20)
  const lists = ctl.listCalls
  await seconds($, clock, 5)
  expect(ctl.listCalls).toBe(lists)
})

test('a row that says waiting again right after a flip puts the wait back', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await openDialog($, on, clock)
  await seconds($, clock, 1)
  ctl.agents = agentRows('busy')
  ctl.sessionFiles++
  await seconds($, clock, 3)
  expect(sent.at(-1)?.body.state).toBe('running')
  ctl.agents = agentRows('waiting', 'permission prompt')
  ctl.sessionFiles++
  await seconds($, clock, 3)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'waiting', message: 'permission prompt' }))
})

test('the poll stops 15 s after a flip that nothing contradicts', async ($, on) => {
  const { clock, ctl } = world(on)
  await openDialog($, on, clock)
  await seconds($, clock, 1)
  ctl.agents = agentRows('busy')
  ctl.sessionFiles++
  await seconds($, clock, 3)
  await seconds($, clock, 14)
  const lists = ctl.listCalls
  expect(lists).toBeGreaterThan(0)
  await seconds($, clock, 4)
  const after = ctl.listCalls
  await seconds($, clock, 5)
  expect(ctl.listCalls).toBe(after)
})

test('a classic PermissionRequest wait is ended the same way', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.classic.PermissionRequest({ tool_name: 'Edit', tool_input: { file_path: '/x' }, session_id: 'sess-1', cwd: `${HOME}/proj`, permission_mode: 'auto' })
  await clock.settle()
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 3)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Edit' }))
})

test('a failed call drops the sightings, stops the timer for 5 minutes, then a lone busy row cannot flip at once', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await openDialog($, on, clock)
  await seconds($, clock, 1)
  ctl.agents = agentRows('busy')
  ctl.sessionFiles++
  await seconds($, clock, 1)
  ctl.agentsFail = true
  ctl.sessionFiles++
  await seconds($, clock, 1)
  expect(agentCalls(ctl)).toBe(2)
  const lists = ctl.listCalls
  await seconds($, clock, 60)
  expect(ctl.listCalls).toBe(lists)
  ctl.agentsFail = false
  await clock.advance(250_000)
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 1)
  expect(sent.at(-1)?.body.state).toBe('waiting')
})

test('a CLI older than 2.1.288 is never asked for agents and its version is checked once', async ($, on) => {
  const { clock, ctl } = world(on)
  ctl.claudeVersion = '2.1.287 (Claude Code)\n'
  await openDialog($, on, clock)
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 2)
  const versions = ctl.runs.filter(argv => argv[1] === '--version').length
  expect(versions).toBeGreaterThan(0)
  expect(agentCalls(ctl)).toBe(0)
  await clock.advance(300_000)
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 2)
  expect(ctl.runs.filter(argv => argv[1] === '--version')).toHaveLength(versions)
})

test('a new ask between sightings starts the count again', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.agents = agentRows('busy')
  await openDialog($, on, clock)
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 1)
  await $.classic.PermissionRequest({ tool_name: 'Edit', tool_input: { file_path: '/x' }, session_id: 'sess-1', cwd: `${HOME}/proj` })
  await seconds($, clock, 1)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'waiting', message: 'approve Edit' }))
  ctl.sessionFiles++
  await seconds($, clock, 3)
  expect(sent.at(-1)?.body).toEqual(expect.objectContaining({ state: 'running', message: 'Edit' }))
})

test('a wait restored from $.state after a reload is polled without waiting for the next event', async ($, on) => {
  const held = heldState(on)
  const { sent, clock, ctl } = world(on)
  const at = clock.now()
  held.value = {
    v: 1 as const,
    sessions: [
      [
        'sess-1',
        {
          state: 'waiting',
          message: 'approve Bash',
          activity: '',
          project: 'proj',
          changedAt: at - 3000,
          hookAt: at - 3000,
          pendingPermission: true,
          pendingTool: 'Bash',
          pendingToolUseId: 'toolu_1',
          askedTool: 'Bash',
          askedToolUseId: 'toolu_1',
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
  held.version = 1
  ctl.agents = agentRows('busy')
  await $.session.start(START)
  await clock.settle()
  await seconds($, clock, 1)
  ctl.sessionFiles++
  await seconds($, clock, 3)
  expect(sent.at(-1)?.body.state).toBe('running')
})

const RESET = '2026-05-12T19:37:13Z'
const RESET_AT = 1778614633
const RESET_LABEL = (() => {
  const d = new Date(RESET_AT * 1000)
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0')
})()
const MEASURED = {
  context: { tokens: 83200, window: 200000, percent: 41.6 },
  rateLimits: [
    { kind: 'five_hour', percentUsed: 22.4, resetsAt: RESET },
    { kind: 'seven_day', percentUsed: 61, resetsAt: '2026-05-19T15:00:00Z' },
  ],
  cost: { usd: 0.42 },
  changed: ['context', 'rateLimits', 'cost'] as ('context' | 'rateLimits' | 'cost')[],
}
const GAUGES = { context_pct: 42, rate_window_pct: 22, rate_reset_at: RESET_AT, rate_reset_label: RESET_LABEL }
const gaugeFields = (b: Record<string, unknown>) =>
  Object.fromEntries(['context_pct', 'rate_window_pct', 'rate_reset_at', 'rate_reset_label', 'rate_week_pct', 'cost'].filter(k => k in b).map(k => [k, b[k]]))

test('session.measure sends nothing itself; the next heartbeat carries the gauges', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const echoed = await $.session.measure(MEASURED)
  await clock.settle()
  expect(echoed.changed).toEqual(MEASURED.changed)
  expect(sent).toHaveLength(1)
  expect(gaugeFields(sent[0]?.body ?? {})).toEqual({})
  await clock.advance(10_000)
  expect(sent).toHaveLength(2)
  expect(gaugeFields(sent[1]?.body ?? {})).toEqual(GAUGES)
})

test('the gauges stick to every later hook POST of the session', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.session.measure(MEASURED)
  await clock.settle()
  await $.session.measure({ context: { window: 200000 }, rateLimits: [], changed: ['context'] })
  await $.classic.Stop(STOP)
  await clock.settle()
  expect(sent.map(s => [s.body.state, gaugeFields(s.body)])).toEqual([
    ['running', {}],
    ['done', GAUGES],
  ])
})

test('a measure before the first prompt posts nothing and is not kept', async ($, on) => {
  const { sent, clock } = world(on)
  await $.session.start(START)
  await $.session.measure(MEASURED)
  await clock.advance(10_000)
  expect(sent).toEqual([])
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(gaugeFields(sent[0]?.body ?? {})).toEqual({})
})

test('EMBER_CONTEXT_PCT_ENABLED=off in producer.env drops context_pct and keeps the rate gauges', async ($, on) => {
  const { sent, clock } = world(on, { ...TRUSTED, text: ENV_TEXT + 'EMBER_CONTEXT_PCT_ENABLED=off\n' })
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.session.measure(MEASURED)
  await clock.advance(10_000)
  expect(gaugeFields(sent.at(-1)?.body ?? {})).toEqual({ rate_window_pct: 22, rate_reset_at: RESET_AT, rate_reset_label: RESET_LABEL })
})

test('session.start reads $.session.usage() once so a session restored from $.state gets its gauges', async ($, on) => {
  const held = heldState(on)
  const { sent, clock, ctl } = world(on)
  ctl.usage = { startedAt: 0, context: MEASURED.context, rateLimits: MEASURED.rateLimits, cost: MEASURED.cost }
  const at = clock.now()
  held.value = {
    v: 1,
    sessions: [['sess-1', { state: 'running', message: 'before reload', project: 'proj', changedAt: at - 5000, hookAt: at - 5000 }]],
    strays: [],
    permissionMode: '',
    t3: '',
  }
  held.version = 1
  await $.session.start(START)
  await clock.advance(10_000)
  expect(ctl.usageCalls).toBe(1)
  expect(sent.map(s => [s.body.message, gaugeFields(s.body)])).toEqual([['before reload', GAUGES]])
})

test('the gauges survive a hot reload through $.state', async ($, on) => {
  const held = heldState(on)
  const { clock } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.session.measure(MEASURED)
  await clock.settle()
  const sessions = (held.value as { sessions: [string, Record<string, unknown>][] }).sessions
  expect(sessions.map(([id, s]) => [id, s.contextPct, s.ratePct, s.rateResetAt, s.rateResetLabel])).toEqual([
    ['sess-1', 42, 22, RESET_AT, RESET_LABEL],
  ])
})

test('a measure whose session lookup fails still answers and posts nothing', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  ctl.sessionIdFails = true
  await $.session.start(START)
  const echoed = await $.session.measure(MEASURED)
  await clock.advance(10_000)
  expect(echoed.changed).toEqual(MEASURED.changed)
  expect(sent).toEqual([])
})

test('a usage() read that answers after a newer session.measure does not overwrite it', async ($, on) => {
  const held = heldState(on)
  const { sent, clock, ctl } = world(on)
  const at = clock.now()
  held.value = {
    v: 1,
    sessions: [['sess-1', { state: 'running', message: 'before reload', project: 'proj', changedAt: at - 5000, hookAt: at - 5000 }]],
    strays: [],
    permissionMode: '',
    t3: '',
  }
  held.version = 1
  const late = gate()
  ctl.usageHold = late
  ctl.usage = { startedAt: 0, context: { window: 200000, percent: 20 }, rateLimits: [{ kind: 'five_hour', percentUsed: 5, resetsAt: RESET }] }
  await $.session.start(START)
  await clock.settle()
  expect(ctl.usageCalls).toBe(1)
  await $.session.measure(MEASURED)
  await clock.settle()
  late.open()
  await clock.advance(10_000)
  expect(gaugeFields(sent.at(-1)?.body ?? {})).toEqual(GAUGES)
})

test('after /compact forgets the session, its next hook reads usage() so the heartbeat has gauges before the turn ends', async ($, on) => {
  const { sent, clock, ctl } = world(on)
  await $.session.start(START)
  await $.classic.UserPromptSubmit(PROMPT)
  await $.session.measure(MEASURED)
  await clock.settle()
  ctl.usage = { startedAt: 0, context: { window: 200000, percent: 3 }, rateLimits: MEASURED.rateLimits }
  await $.classic.SessionStart({ source: 'compact', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  const before = ctl.usageCalls
  await clock.advance(10_000)
  expect(gaugeFields(sent.at(-1)?.body ?? {})).toEqual({ ...GAUGES, context_pct: 3 })
  await $.classic.UserPromptSubmit(PROMPT)
  await clock.settle()
  expect(ctl.usageCalls).toBe(before)
})
