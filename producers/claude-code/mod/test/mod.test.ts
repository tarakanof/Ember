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

function world(on: On, envFile: EnvFile | null = TRUSTED) {
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
  }
  mock.env(on, { HOME })
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
