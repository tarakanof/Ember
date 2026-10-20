import type { EngineInterface, Register } from 'claude-code'
import {
  HEARTBEAT_MS,
  Sender,
  SESSION_END_BUDGET_MS,
  TICK_SEND_TIMEOUT_MS,
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
  type ConfigResult,
  type Effect,
  type FetchLike,
} from './producer.ts'

type Engine = EngineInterface

type ModState = {
  tracker: Tracker
  sender: Sender
  config: Config | undefined
  loading: Promise<void> | undefined
  beating: boolean
  uid: string | undefined
  interactive: boolean
  permissionMode: string
  heartbeat: { cancel: () => void } | undefined
}

type Step = (cfg: Config, now: number, id: string) => Effect[]

const STAT = ['/usr/bin/stat', '/bin/stat', 'stat']
const ID = ['/usr/bin/id', '/bin/id', 'id']

async function readText($: Engine, path: string): Promise<string | undefined> {
  try {
    return await $.fs.read(path)
  } catch {
    return undefined
  }
}

async function firstOutput($: Engine, argvs: string[][]): Promise<string> {
  for (const argv of argvs) {
    try {
      const r = await $.process.run(argv, { timeoutMs: 2000 })
      if (r.exitCode === 0) return r.stdout
    } catch {
      continue
    }
  }
  return ''
}

async function currentUID($: Engine, st: ModState): Promise<string> {
  if (st.uid === undefined) {
    const uid = (await firstOutput($, ID.map(bin => [bin, '-u']))).trim()
    if (uid !== '') st.uid = uid
    return uid
  }
  return st.uid
}

async function readEnvFile($: Engine, st: ModState, path: string): Promise<{ text: string; problem: string }> {
  let info: { kind: string; isLink: boolean }
  try {
    info = await $.fs.stat(path)
  } catch {
    return { text: '', problem: '' }
  }
  if (info.isLink || info.kind !== 'file') return { text: '', problem: 'is not a regular file' }
  const uid = await currentUID($, st)
  if (uid === '') throw new Error('id -u gave no answer')
  const statOut = await firstOutput(
    $,
    STAT.flatMap(bin => [
      [bin, '-f', '%Lp %u', path],
      [bin, '-c', '%a %u', path],
    ]),
  )
  if (statOut.trim() === '') throw new Error('stat gave no answer')
  const problem = envFileProblem(statOut, uid)
  if (problem !== '') return { text: '', problem }
  return { text: (await readText($, path)) ?? '', problem: '' }
}

function killSwitchPath(home: string): string {
  return `${home}/.config/ember/claude-hooks.disabled`
}

async function resolve($: Engine, st: ModState): Promise<ConfigResult> {
  const home = (await $.env.get('HOME')) ?? ''
  if (home === '') return { disabled: 'HOME is not set' }
  if (await $.fs.exists(killSwitchPath(home))) return { disabled: 'claude-hooks.disabled is present' }
  const envFile = await readEnvFile($, st, `${home}/.config/ember/producer.env`)
  const xdg = (await $.env.get('XDG_STATE_HOME')) ?? ''
  return resolveConfig({
    env: parseEnvFile(envFile.text),
    home,
    processToken: await $.env.get('EMBER_TOKEN'),
    serverCache: await readText($, `${stateHome(home, xdg)}/ember/server.json`),
    envFileProblem: envFile.problem,
  })
}

async function load($: Engine, st: ModState): Promise<void> {
  const result = await resolve($, st)
  st.config = 'config' in result ? result.config : undefined
}

async function liveConfig($: Engine, st: ModState): Promise<Config | undefined> {
  if (!st.config && st.loading) await st.loading
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

function emit($: Engine, st: ModState, cfg: Config, effects: Effect[], limitMs: number, heartbeat = false): Promise<void>[] {
  const io = {
    deliver: (effect: Effect) => send(fetcher($), cfg, effect),
    timer: (ms: number) => $.clock.sleep(ms),
    gone: (session: string) => st.tracker.gone(session),
  }
  return effects.map(effect => st.sender.push(effect, limitMs, io, heartbeat))
}

async function track($: Engine, st: ModState, rawSession: string, cwd: string, step: Step): Promise<void> {
  start($, st, false)
  const cfg = await liveConfig($, st)
  if (!cfg) return
  const id = await sessionID(rawSession, cwd)
  emit($, st, cfg, step(cfg, await $.clock.now(), id), cfg.hookTimeoutMs)
}

async function trackLive($: Engine, st: ModState, step: (cfg: Config, now: number, id: string, cwd: string) => Effect[]): Promise<void> {
  const raw = await $.session.id()
  const cwd = await $.session.cwd()
  await track($, st, raw, cwd, (cfg, now, id) => step(cfg, now, id, cwd))
}

async function beat($: Engine, st: ModState): Promise<void> {
  if (st.beating) return
  st.beating = true
  try {
    await quietly(() => load($, st))
    const cfg = st.config
    if (cfg) emit($, st, cfg, st.tracker.tick(cfg, await $.clock.now()), TICK_SEND_TIMEOUT_MS, true)
  } finally {
    st.beating = false
  }
}

async function endSession($: Engine, st: ModState, rawSession: string, cwd: string, reason: string): Promise<void> {
  const cfg = await liveConfig($, st)
  if (!cfg) return
  const sent = emit($, st, cfg, st.tracker.sessionEnd(cfg, await sessionID(rawSession, cwd), reason), cfg.hookTimeoutMs)
  if (sent.length === 0) return
  await Promise.race([Promise.all(sent), $.clock.sleep(SESSION_END_BUDGET_MS)])
}

async function quietly(work: () => Promise<void>): Promise<void> {
  try {
    await work()
  } catch {
    return
  }
}

function start($: Engine, st: ModState, restart: boolean): void {
  if (st.heartbeat && !restart) return
  st.heartbeat?.cancel()
  st.loading = quietly(() => load($, st))
  st.heartbeat = $.clock.every(HEARTBEAT_MS, () => void quietly(() => beat($, st)))
}

function noteMode(st: ModState, mode: string | undefined): void {
  if (mode) st.permissionMode = mode
}

export const register: Register = on => {
  const st: ModState = {
    tracker: new Tracker(),
    sender: new Sender(),
    config: undefined,
    loading: undefined,
    beating: false,
    uid: undefined,
    interactive: false,
    permissionMode: '',
    heartbeat: undefined,
  }
  const t = st.tracker

  on('session.start', async ($, e, next) => {
    st.interactive = e.isInteractive
    start($, st, true)
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
    await quietly(() => trackLive($, st, (cfg, now, id, cwd) => t.preToolUse(cfg, now, id, e.tool, e, e.tool_use_id, cwd)))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('tool.check', async ($, e, next) => {
    const verdict = await next(e)
    const toolUseId = e.tool_use_id
    if (verdict.decision === 'ask' && verdict.decider === undefined && toolUseId !== undefined) {
      const dialog = dialogExpected(st.interactive, st.permissionMode)
      await quietly(() => trackLive($, st, (cfg, now, id, cwd) => t.toolAsked(cfg, now, id, e.tool, e.input, toolUseId, dialog, cwd)))
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
