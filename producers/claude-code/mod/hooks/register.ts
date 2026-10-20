import type { EngineInterface, Register } from 'claude-code'
import { T3_PROBE_BUDGET_MS, T3_RETRY_MS, argvFromPs, detectT3, parsePsInfo, type T3Probe, type T3Verdict } from './t3.ts'
import {
  AGENTS_CALL_TIMEOUT_MS,
  AGENTS_FAIL_BACKOFF_MS,
  AGENTS_FALLBACK_MS,
  AGENTS_WATCH_MS,
  FieldDropper,
  HEARTBEAT_MS,
  Sender,
  SESSION_END_BUDGET_MS,
  TICK_SEND_TIMEOUT_MS,
  Tracker,
  agentsCLIUsable,
  dialogExpected,
  envFileProblem,
  failureOutcome,
  gaugesFromUsage,
  isPlaceholderSource,
  bounded,
  normalizeHostName,
  parseAgents,
  parseEnvFile,
  parseSnapshot,
  resolveConfig,
  send,
  sessionID,
  stateHome,
  type Config,
  type ConfigResult,
  type Effect,
  type FetchLike,
  type Snapshot,
  type UsageReading,
} from './producer.ts'

type Engine = EngineInterface

const SNAPSHOT = { plugin: 'ember-mod', key: 'snapshot' } as const

type ModState = {
  tracker: Tracker
  sender: Sender
  config: Config | undefined
  loading: Promise<void> | undefined
  beating: boolean
  uid: string | undefined
  host: string
  hostTriedAt: number | undefined
  dropper: FieldDropper
  interactive: boolean
  permissionMode: string
  heartbeat: { cancel: () => void } | undefined
  watch: { cancel: () => void } | undefined
  watching: boolean
  backingOff: boolean
  lastPrint: string | undefined
  lastCall: number
  cliVerdict: Map<string, boolean>
  claudeBin: string | undefined
  restored: Promise<void> | undefined
  saving: boolean
  dirty: boolean
  t3: T3Verdict
  t3TriedAt: number | undefined
}

type Step = (cfg: Config, now: number, id: string) => Effect[]

const STAT = ['/usr/bin/stat', '/bin/stat', 'stat']
const HOST_COMMANDS = [
  [['/usr/sbin/scutil', '--get', 'LocalHostName'], ['scutil', '--get', 'LocalHostName']],
  [['/bin/hostname'], ['/usr/bin/hostname'], ['hostname']],
]
const HOST_TIMEOUT_MS = 150
const HOST_RETRY_MS = 600_000
const ID = ['/usr/bin/id', '/bin/id', 'id']
const PS = ['/bin/ps', '/usr/bin/ps', 'ps']
const SH = ['/bin/sh', '/usr/bin/sh', 'sh']

async function readText($: Engine, path: string): Promise<string | undefined> {
  try {
    return await $.fs.read(path)
  } catch {
    return undefined
  }
}

async function firstOutput($: Engine, argvs: string[][], timeoutMs = 2000): Promise<string> {
  for (const argv of argvs) {
    try {
      const r = await $.process.run(argv, { timeoutMs })
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

async function hostName($: Engine, st: ModState): Promise<string> {
  const now = await $.clock.now()
  if (st.hostTriedAt !== undefined && (st.host !== '' || now - st.hostTriedAt < HOST_RETRY_MS)) return st.host
  st.hostTriedAt = now
  st.host = ''
  for (const argvs of HOST_COMMANDS) {
    const name = normalizeHostName(await firstOutput($, argvs, HOST_TIMEOUT_MS))
    if (name !== '') {
      st.host = name
      break
    }
  }
  return st.host
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
  const env = parseEnvFile(envFile.text)
  const needsHost = (env.EMBER_MOD_SOURCE ?? '').trim() === '' && isPlaceholderSource(env.EMBER_SOURCE)
  return resolveConfig({
    env,
    hostName: needsHost ? await hostName($, st) : undefined,
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

function emit($: Engine, st: ModState, cfg: Config, effects: Effect[], limitMs: number, now: number, heartbeat = false): Promise<void>[] {
  const io = {
    deliver: (effect: Effect) => send(fetcher($), cfg, effect, st.dropper),
    timer: (ms: number) => $.clock.sleep(ms),
    reconcile: async (session: string) => {
      const at = await $.clock.now()
      return st.tracker.reconcile(cfg, at, session)
    },
  }
  return effects.map(effect => st.sender.push(effect, limitMs, io, now, heartbeat))
}

async function restore($: Engine, st: ModState): Promise<void> {
  const snap = parseSnapshot((await $.state.get(SNAPSHOT)).value)
  if (!snap) return
  const now = await $.clock.now()
  st.tracker.restore(snap.sessions)
  st.sender.restore(snap.strays, now)
  if (st.permissionMode === '') st.permissionMode = snap.permissionMode
  if (st.t3 === 'unknown' && snap.t3 !== '') st.t3 = snap.t3
  void st.loading?.then(() => syncWatch($, st))
}

function ready($: Engine, st: ModState): Promise<void> {
  st.restored ??= quietly(() => restore($, st))
  return st.restored
}

function snapshot(st: ModState): Snapshot {
  return {
    v: 1,
    sessions: st.tracker.entries(),
    strays: st.sender.entries(),
    permissionMode: st.permissionMode,
    t3: st.t3 === 'unknown' ? '' : st.t3,
  }
}

function persist($: Engine, st: ModState): void {
  syncWatch($, st)
  st.dirty = true
  if (st.saving) return
  st.saving = true
  void (async () => {
    try {
      while (st.dirty) {
        st.dirty = false
        await quietly(async () => void (await $.state.set(SNAPSHOT, snapshot(st))))
      }
    } finally {
      st.saving = false
    }
  })()
}

function claudeCandidates(home: string): string[] {
  return [`${home}/.local/bin/claude`, 'claude', '/opt/homebrew/bin/claude', '/usr/local/bin/claude']
}

async function agentsOutput($: Engine, st: ModState, home: string): Promise<string | undefined> {
  const run = async (argv: string[]) => {
    try {
      const r = await $.process.run(argv, { timeoutMs: AGENTS_CALL_TIMEOUT_MS })
      return r.exitCode === 0 ? r.stdout : undefined
    } catch {
      return undefined
    }
  }
  if (st.claudeBin === undefined) {
    for (const bin of claudeCandidates(home)) {
      let usable = st.cliVerdict.get(bin)
      if (usable === undefined) {
        usable = agentsCLIUsable((await run([bin, '--version'])) ?? '')
        st.cliVerdict.set(bin, usable)
      }
      if (usable) {
        st.claudeBin = bin
        break
      }
    }
  }
  if (st.claudeBin === undefined) return undefined
  const out = await run([st.claudeBin, 'agents', '--json'])
  if (out === undefined) {
    st.cliVerdict.delete(st.claudeBin)
    st.claudeBin = undefined
  }
  return out
}

async function sessionsFingerprint($: Engine, home: string): Promise<string> {
  const dir = (((await $.env.get('CLAUDE_CONFIG_DIR')) ?? '') || `${home}/.claude`) + '/sessions'
  try {
    const entries = await $.fs.list(dir)
    return entries
      .filter(e => e.name.endsWith('.json'))
      .map(e => `${e.name}:${e.mtimeMs}:${e.size}`)
      .sort()
      .join('|')
  } catch {
    return ''
  }
}

function stopWatch(st: ModState): void {
  st.watch?.cancel()
  st.watch = undefined
  st.lastPrint = undefined
  st.lastCall = 0
}

function syncWatch($: Engine, st: ModState): void {
  const wanted = st.config !== undefined && st.t3 !== 'yes' && !st.backingOff && st.tracker.needsWatch()
  if (wanted && !st.watch) {
    st.watch = $.clock.every(AGENTS_WATCH_MS, () => void quietly(() => watchPass($, st)))
  } else if (!wanted && st.watch) {
    stopWatch(st)
  }
}

function failWatch($: Engine, st: ModState): void {
  st.tracker.agentsFailed()
  st.backingOff = true
  stopWatch(st)
  $.clock.after(AGENTS_FAIL_BACKOFF_MS, () => {
    st.backingOff = false
    syncWatch($, st)
  })
}

async function watchPass($: Engine, st: ModState): Promise<void> {
  if (st.watching) return
  st.watching = true
  try {
    const cfg = await liveConfig($, st)
    const began = await $.clock.now()
    st.tracker.observe(began)
    if (!cfg || !st.tracker.needsWatch()) {
      syncWatch($, st)
      return
    }
    const print = await sessionsFingerprint($, cfg.home)
    const changed = st.lastPrint !== undefined && print !== st.lastPrint
    st.lastPrint = print
    if (st.lastCall === 0) st.lastCall = began
    if (!changed && !st.tracker.confirmDue(began) && began - st.lastCall < AGENTS_FALLBACK_MS) return
    st.lastCall = began
    const out = await agentsOutput($, st, cfg.home)
    const rows = out === undefined ? undefined : parseAgents(out)
    const now = await $.clock.now()
    if (!rows) {
      failWatch($, st)
      return
    }
    const effects = st.tracker.agentsCorrect(cfg, began, now, rows)
    if (effects.length > 0) emit($, st, cfg, effects, cfg.hookTimeoutMs, now)
    persist($, st)
  } finally {
    st.watching = false
  }
}

async function runFirst($: Engine, argvs: string[][], timeoutMs: number): Promise<string | undefined> {
  for (const argv of argvs) {
    try {
      const r = await $.process.run(argv, { timeoutMs })
      if (r.exitCode === 0) return r.stdout
    } catch {
      continue
    }
  }
  return undefined
}

function t3Probe($: Engine): T3Probe {
  const limit = T3_PROBE_BUDGET_MS
  return {
    isSDK: async () =>
      (await $.env.get('CLAUDE_CODE_ENTRYPOINT')) === 'sdk-ts' || ((await $.env.get('CLAUDE_AGENT_SDK_VERSION')) ?? '') !== '',
    start: async () => {
      const out = await runFirst($, SH.map(sh => [sh, '-c', 'echo $PPID']), limit)
      const pid = Number((out ?? '').trim())
      return Number.isInteger(pid) && pid > 0 ? pid : undefined
    },
    info: async pid => {
      const out = await runFirst($, PS.map(ps => [ps, '-o', 'ppid=,comm=', '-p', String(pid)]), limit)
      return out === undefined ? undefined : parsePsInfo(out)
    },
    argv: async pid => {
      const proc = await runFirst($, SH.map(sh => [sh, '-c', 'tr "\\0" "\\n" < /proc/$1/cmdline', 'sh', String(pid)]), limit)
      if (proc !== undefined && proc.trim() !== '') return proc.replace(/\n$/, '').split('\n')
      const comm = await runFirst($, PS.map(ps => [ps, '-ww', '-o', 'comm=', '-p', String(pid)]), limit)
      const args = await runFirst($, PS.map(ps => [ps, '-ww', '-o', 'args=', '-p', String(pid)]), limit)
      return comm === undefined || args === undefined || args.trim() === '' ? undefined : argvFromPs(comm, args)
    },
  }
}

async function silencedByT3($: Engine, st: ModState, cfg: Config): Promise<boolean> {
  if (st.t3 === 'yes') return true
  if (st.t3 === 'no') return false
  const now = await $.clock.now()
  if (st.t3TriedAt !== undefined && now - st.t3TriedAt < T3_RETRY_MS) return false
  st.t3TriedAt = now
  st.t3 = await bounded(detectT3(t3Probe($)), $.clock.sleep(T3_PROBE_BUDGET_MS), 'unknown' as T3Verdict).catch(() => 'unknown' as T3Verdict)
  if (st.t3 !== 'yes') {
    if (st.t3 === 'no') persist($, st)
    return false
  }
  const gone = [...st.tracker.sessions.keys()]
  st.tracker.sessions.clear()
  for (const id of gone) emit($, st, cfg, [{ method: 'DELETE', body: { source: cfg.source, tool: 'claude', session: id } }], cfg.hookTimeoutMs, now)
  persist($, st)
  return true
}

async function track($: Engine, st: ModState, rawSession: string, cwd: string, step: Step): Promise<void> {
  start($, st, false)
  await ready($, st)
  const cfg = await liveConfig($, st)
  if (!cfg) return
  if (await silencedByT3($, st, cfg)) return
  const id = await sessionID(rawSession, cwd)
  const now = await $.clock.now()
  emit($, st, cfg, step(cfg, now, id), cfg.hookTimeoutMs, now)
  persist($, st)
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
    await ready($, st)
    await quietly(() => load($, st))
    const cfg = st.config
    const now = await $.clock.now()
    if (cfg && st.t3 !== 'yes') {
      emit($, st, cfg, st.tracker.tick(cfg, now), TICK_SEND_TIMEOUT_MS, now, true)
      persist($, st)
    }
  } finally {
    st.beating = false
  }
}

async function endSession($: Engine, st: ModState, rawSession: string, cwd: string, reason: string): Promise<void> {
  await ready($, st)
  const cfg = await liveConfig($, st)
  if (!cfg) return
  if (await silencedByT3($, st, cfg)) return
  const id = await sessionID(rawSession, cwd)
  const now = await $.clock.now()
  const sent = emit($, st, cfg, st.tracker.sessionEnd(cfg, id, reason), cfg.hookTimeoutMs, now)
  persist($, st)
  if (sent.length === 0) return
  await Promise.race([Promise.all(sent), $.clock.sleep(SESSION_END_BUDGET_MS)])
}

async function measure($: Engine, st: ModState, reading: UsageReading | undefined): Promise<void> {
  await ready($, st)
  const usage = reading ?? (await $.session.usage())
  const id = await sessionID(await $.session.id(), await $.session.cwd())
  st.tracker.measure(id, gaugesFromUsage(usage))
  persist($, st)
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
    host: '',
    hostTriedAt: undefined,
    dropper: new FieldDropper(),
    interactive: false,
    permissionMode: '',
    heartbeat: undefined,
    watch: undefined,
    watching: false,
    backingOff: false,
    lastPrint: undefined,
    lastCall: 0,
    cliVerdict: new Map(),
    claudeBin: undefined,
    restored: undefined,
    saving: false,
    dirty: false,
    t3: 'unknown',
    t3TriedAt: undefined,
  }
  const t = st.tracker

  on('session.start', async ($, e, next) => {
    st.interactive = e.isInteractive
    start($, st, true)
    await quietly(() => ready($, st))
    void quietly(() => measure($, st, undefined))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('session.measure', ($, e, next) => {
    void quietly(() => measure($, st, e))
    return next(e)
  }).catch(($, e, next) => next(e))

  on('classic.SessionStart', async ($, e, next) => {
    await quietly(async () => {
      await ready($, st)
      noteMode(st, e.permission_mode)
      t.sessionStart(await sessionID(e.session_id, e.cwd), e.source)
      persist($, st)
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
