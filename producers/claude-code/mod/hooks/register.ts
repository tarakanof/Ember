import type { EngineInterface, Register } from 'claude-code'
import {
  HEARTBEAT_MS,
  Tracker,
  dialogExpected,
  envFileProblem,
  failureOutcome,
  parseEnvFile,
  resolveConfig,
  send,
  sessionID,
  stateHome,
  type Config,
  type Effect,
  type FetchLike,
} from './producer.ts'

type Engine = EngineInterface

type ModState = {
  tracker: Tracker
  config: Config | undefined
  interactive: boolean
  permissionMode: string
  heartbeat: { cancel: () => void } | undefined
}

type Step = (cfg: Config, now: number, id: string) => Effect[]

async function readText($: Engine, path: string): Promise<string | undefined> {
  try {
    return await $.fs.read(path)
  } catch {
    return undefined
  }
}

async function statLine($: Engine, path: string): Promise<string> {
  for (const argv of [
    ['stat', '-f', '%Lp %u', path],
    ['stat', '-c', '%a %u', path],
  ]) {
    const r = await $.process.run(argv, { timeoutMs: 2000 })
    if (r.exitCode === 0) return r.stdout
  }
  return ''
}

async function readEnvFile($: Engine, path: string): Promise<{ text: string; problem: string }> {
  let info: { kind: string; isLink: boolean }
  try {
    info = await $.fs.stat(path)
  } catch {
    return { text: '', problem: '' }
  }
  if (info.isLink || info.kind !== 'file') return { text: '', problem: 'is not a regular file' }
  const uid = (await $.process.run(['id', '-u'], { timeoutMs: 2000 })).stdout
  const problem = envFileProblem(await statLine($, path), uid)
  if (problem !== '') return { text: '', problem }
  return { text: (await readText($, path)) ?? '', problem: '' }
}

function killSwitchPath(home: string): string {
  return `${home}/.config/ember/claude-hooks.disabled`
}

async function load($: Engine, st: ModState): Promise<void> {
  st.config = undefined
  const home = (await $.env.get('HOME')) ?? ''
  if (home === '') return
  const envFile = await readEnvFile($, `${home}/.config/ember/producer.env`)
  const xdg = (await $.env.get('XDG_STATE_HOME')) ?? ''
  const result = resolveConfig({
    env: parseEnvFile(envFile.text),
    home,
    processToken: await $.env.get('EMBER_TOKEN'),
    serverCache: await readText($, `${stateHome(home, xdg)}/ember/server.json`),
    killSwitch: await $.fs.exists(killSwitchPath(home)),
    envFileProblem: envFile.problem,
  })
  st.config = 'config' in result ? result.config : undefined
}

async function liveConfig($: Engine, st: ModState): Promise<Config | undefined> {
  const cfg = st.config
  if (!cfg) return undefined
  if (await $.fs.exists(killSwitchPath(cfg.home))) {
    st.config = undefined
    return undefined
  }
  return cfg
}

function fetcher($: Engine): FetchLike {
  return (url, init) => $.http.fetch(url, init)
}

function emit($: Engine, st: ModState, effects: Effect[]): void {
  const cfg = st.config
  if (!cfg) return
  for (const effect of effects) void send(fetcher($), cfg, effect)
}

async function track($: Engine, st: ModState, rawSession: string, cwd: string, step: Step): Promise<void> {
  const cfg = await liveConfig($, st)
  if (!cfg) return
  const id = await sessionID(rawSession, cwd)
  emit($, st, step(cfg, await $.clock.now(), id))
}

async function trackLive($: Engine, st: ModState, step: Step): Promise<void> {
  const raw = await $.session.id()
  await track($, st, raw, raw === '' ? await $.session.cwd() : '', step)
}

async function beat($: Engine, st: ModState): Promise<void> {
  await load($, st)
  const cfg = st.config
  if (cfg) emit($, st, st.tracker.tick(cfg, await $.clock.now()))
}

async function endSession($: Engine, st: ModState, rawSession: string, cwd: string, reason: string): Promise<void> {
  const cfg = await liveConfig($, st)
  if (!cfg) return
  const effects = st.tracker.sessionEnd(cfg, await sessionID(rawSession, cwd), reason)
  await Promise.all(effects.map(effect => send(fetcher($), cfg, effect)))
}

async function quietly(work: () => Promise<void>): Promise<void> {
  try {
    await work()
  } catch {
    return
  }
}

function noteMode(st: ModState, mode: string | undefined): void {
  if (mode) st.permissionMode = mode
}

export const register: Register = on => {
  const st: ModState = {
    tracker: new Tracker(),
    config: undefined,
    interactive: false,
    permissionMode: '',
    heartbeat: undefined,
  }
  const t = st.tracker

  on('session.start', async ($, e, next) => {
    st.interactive = e.isInteractive
    await quietly(() => load($, st))
    st.heartbeat?.cancel()
    st.heartbeat = $.clock.every(HEARTBEAT_MS, () => void quietly(() => beat($, st)))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.SessionStart', async ($, e, next) => {
    await quietly(async () => {
      noteMode(st, e.permission_mode)
      t.sessionStart(await sessionID(e.session_id, e.cwd), e.source)
    })
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.UserPromptSubmit', async ($, e, next) => {
    noteMode(st, e.permission_mode)
    await quietly(() => track($, st, e.session_id, e.cwd, (cfg, now, id) => t.promptSubmit(cfg, now, id, e.prompt, e.cwd)))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.PreToolUse', async ($, e, next) => {
    await quietly(() => trackLive($, st, (cfg, now, id) => t.preToolUse(cfg, now, id, e.tool, e, e.tool_use_id)))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('tool.check', async ($, e, next) => {
    const verdict = await next(e)
    const toolUseId = e.tool_use_id
    if (verdict.decision === 'ask' && verdict.decider === undefined && toolUseId !== undefined) {
      const dialog = dialogExpected(st.interactive, st.permissionMode)
      await quietly(() => trackLive($, st, (cfg, now, id) => t.toolAsked(cfg, now, id, e.tool, e.input, toolUseId, dialog)))
    }
    return verdict
  }).catch(($, e, next) => next(e))

  on('classic.PermissionRequest', async ($, e, next) => {
    noteMode(st, e.permission_mode)
    await quietly(() =>
      track($, st, e.session_id, e.cwd, (cfg, now, id) => t.permissionRequest(cfg, now, id, e.tool_name, e.tool_input, e.cwd)),
    )
    return next(e)
  }).catch(($, e, next) => next(e))

  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    await quietly(() => trackLive($, st, (cfg, now, id) => t.toolFinished(cfg, now, id, e.tool, e.tool_use_id)))
    return result
  }).catch(($, e, next) => next(e))

  on('classic.PostToolUseFailure', async ($, e, next) => {
    const outcome = failureOutcome(e.error, e.is_interrupt === true)
    await quietly(() => track($, st, e.session_id, e.cwd, (cfg, _now, id) => t.toolOutcome(cfg, id, e.tool_name, e.tool_input, outcome)))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.PermissionDenied', async ($, e, next) => {
    await quietly(() => track($, st, e.session_id, e.cwd, (cfg, _now, id) => t.toolOutcome(cfg, id, e.tool_name, e.tool_input, 'denied')))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.Notification', async ($, e, next) => {
    await quietly(() =>
      track($, st, e.session_id, e.cwd, (cfg, now, id) => t.notification(cfg, now, id, e.notification_type, e.message, e.cwd)),
    )
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.Stop', async ($, e, next) => {
    noteMode(st, e.permission_mode)
    await quietly(() =>
      track($, st, e.session_id, e.cwd, (cfg, now, id) =>
        t.stop(cfg, now, id, e.last_assistant_message ?? '', e.background_tasks, e.cwd),
      ),
    )
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.StopFailure', async ($, e, next) => {
    await quietly(() =>
      track($, st, e.session_id, e.cwd, (cfg, now, id) =>
        t.stopFailure(cfg, now, id, String(e.error ?? ''), e.error_details ?? '', e.last_assistant_message ?? '', e.cwd),
      ),
    )
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.SessionEnd', async ($, e, next) => {
    await quietly(() => endSession($, st, e.session_id, e.cwd, e.reason))
    return next(e)
  }).catch(($, e, next) => next(e))
}
