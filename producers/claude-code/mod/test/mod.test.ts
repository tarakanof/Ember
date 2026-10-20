import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/Users/tester'
const ENV_FILE = `${HOME}/.config/ember/producer.env`
const ENV_TEXT = 'EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://127.0.0.1:9\nEMBER_TOKEN=test-token\n'

type Sent = { url: string; method: string; auth: string; body: Record<string, unknown> }

type EnvFile = { text: string; mode: string; owner: string; isLink: boolean }

const TRUSTED: EnvFile = { text: ENV_TEXT, mode: '600', owner: '501', isLink: false }

function world(on: On, envFile: EnvFile | null = TRUSTED) {
  const sent: Sent[] = []
  const files = { killSwitch: false }
  mock.env(on, { HOME })
  const clock = mock.clock(on, { now: 1_000_000 })
  on('fs.read', ($, e) => (e.path === ENV_FILE && envFile !== null ? { value: envFile.text } : { deny: 'ENOENT' }))
  on('fs.stat', ($, e) =>
    e.path === ENV_FILE && envFile !== null
      ? { value: { kind: 'file' as const, size: envFile.text.length, mtimeMs: 0, isLink: envFile.isLink } }
      : { deny: 'ENOENT' },
  )
  on('process.run', ($, e) => {
    if (e.argv[0] === 'id') return { value: { exitCode: 0, stdout: '501\n', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
    const ok = e.argv[1] === '-f' && envFile !== null
    return {
      value: {
        exitCode: ok ? 0 : 1,
        stdout: ok ? `${envFile?.mode} ${envFile?.owner}\n` : '',
        stderr: '',
        isStdoutTruncated: false,
        isStderrTruncated: false,
      },
    }
  })
  on('fs.exists', ($, e) => ({ value: e.path.endsWith('/claude-hooks.disabled') && files.killSwitch }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('classic.*', () => ({}))
  on('session.id', () => ({ value: 'sess-1' }))
  on('session.cwd', () => ({ value: `${HOME}/proj` }))
  on('http.fetch', ($, e) => {
    sent.push({
      url: e.url,
      method: e.init?.method ?? 'GET',
      auth: e.init?.headers?.Authorization ?? '',
      body: JSON.parse(e.init?.body ?? '{}') as Record<string, unknown>,
    })
    return { value: { status: 200, ok: true, headers: {}, text: '{"ok":true}' } }
  })
  return { sent, clock, files }
}

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
  const { sent, clock, files } = world(on)
  await $.session.start({ cwd: `${HOME}/proj`, surface: 'terminal', isInteractive: true })
  await $.classic.UserPromptSubmit({ prompt: 'go', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.settle()
  files.killSwitch = true
  await $.classic.Stop({ stop_hook_active: false, last_assistant_message: 'done', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await $.classic.SessionEnd({ reason: 'prompt_input_exit', session_id: 'sess-1', cwd: `${HOME}/proj` })
  await clock.advance(10_000)
  expect(sent.map(s => s.body.state)).toEqual(['running'])
})
