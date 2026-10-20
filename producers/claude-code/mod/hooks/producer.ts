export type Env = Record<string, string>

export type Config = {
  source: string
  serverURL: string
  token: string
  sourceColor: string
  activityTrail: boolean
  activityDetail: boolean
  sourceCard: boolean
  sessionBar: boolean
  sendProject: boolean
  contextNumber: boolean
  rateBottomBar: boolean
  rateReset: boolean
  doneTTLSeconds: number
  heartbeatTTLHours: number
  hookTimeoutMs: number
  home: string
}

export type ConfigInputs = {
  env: Env
  home: string
  processToken?: string
  serverCache?: string
  killSwitch?: boolean
  envFileProblem?: string
}

export type ConfigResult = { config: Config } | { disabled: string }

export type Wire = {
  source: string
  tool: 'claude'
  session: string
  state: string
  message?: string
  source_color?: string
  activity?: string
  context_number?: true
  rate_bottom_bar?: true
  rate_reset?: true
  source_card: boolean
  session_bar: boolean
  project?: string
}

export type SessionKey = { source: string; tool: 'claude'; session: string }

export type Effect = { method: 'POST'; body: Wire } | { method: 'DELETE'; body: SessionKey }

export type FetchLike = (
  url: string,
  init: { method: string; headers: Record<string, string>; body: string },
) => Promise<{ ok: boolean; status: number }>

export const HEARTBEAT_MS = 10_000
export const SESSION_END_BUDGET_MS = 800
export const TICK_SEND_TIMEOUT_MS = 5000
export const WAIT_BACKSTOP_MS = 30 * 60_000
export const RESUME_GRACE_MS = 15_000
export const SOURCE_PLACEHOLDER = 'set-me-to-this-laptop-id'
export const END_REASONS = ['logout', 'prompt_input_exit', 'other', 'clear', 'resume']

const MAX_FIELD = 80
const PROJECT_MAX = 32
const TRAIL_SEPARATOR = ' · '

export function parseEnvFile(text: string): Env {
  const out: Env = {}
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (line === '' || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    const key = line.slice(0, eq).trim()
    let val = line.slice(eq + 1).trim()
    if (val.length >= 2 && (val[0] === '"' || val[0] === "'") && val[val.length - 1] === val[0]) {
      val = val.slice(1, -1)
    }
    out[key] = val
  }
  return out
}

export function envFileProblem(statOutput: string, uid: string): string {
  const [mode, owner] = statOutput.trim().split(/\s+/)
  if (mode !== '600') return `permission must be 0600 (got 0${mode ?? '?'})`
  if (owner === undefined || owner !== uid.trim()) return 'must be owned by the current user'
  return ''
}

export function parseBool(v: string | undefined, def: boolean): boolean {
  switch ((v ?? '').toLowerCase()) {
    case 'true':
    case '1':
    case 'yes':
    case 'on':
      return true
    case 'false':
    case '0':
    case 'no':
    case 'off':
      return false
  }
  return def
}

function positiveInt(v: string | undefined, def: number): number {
  if (v === undefined || !/^[+-]?\d+$/.test(v)) return def
  const n = Number(v)
  return n > 0 ? n : def
}

function isPlaceholderSource(v: string | undefined): boolean {
  const s = (v ?? '').trim()
  return s === '' || s.toLowerCase() === SOURCE_PLACEHOLDER
}

function isAutoServerURL(v: string | undefined): boolean {
  const s = (v ?? '').trim()
  return s === '' || s.toLowerCase() === 'auto'
}

function cachedServerURL(raw: string | undefined, prefer: string): string {
  if (raw === undefined) return ''
  try {
    const c = JSON.parse(raw) as { url?: unknown; prefer?: unknown }
    if (typeof c.url !== 'string' || c.url === '') return ''
    const cachedPrefer = typeof c.prefer === 'string' ? c.prefer : ''
    return cachedPrefer === prefer ? c.url : ''
  } catch {
    return ''
  }
}

export function resolveConfig(inputs: ConfigInputs): ConfigResult {
  if (inputs.killSwitch) return { disabled: 'claude-hooks.disabled is present' }
  if (inputs.envFileProblem) return { disabled: 'producer.env ' + inputs.envFileProblem }
  const env = inputs.env
  const modSource = (env.EMBER_MOD_SOURCE ?? '').trim()
  let source = modSource
  if (source === '') {
    if (isPlaceholderSource(env.EMBER_SOURCE)) return { disabled: 'EMBER_SOURCE is not set' }
    source = (env.EMBER_SOURCE ?? '').trim() + '-mod'
  }
  const prefer = (env.EMBER_SERVER_INSTANCE ?? '').trim()
  const serverURL = isAutoServerURL(env.EMBER_SERVER_URL)
    ? cachedServerURL(inputs.serverCache, prefer)
    : (env.EMBER_SERVER_URL ?? '').trim()
  if (serverURL === '') return { disabled: 'no server URL (set EMBER_SERVER_URL or run discover)' }
  return {
    config: {
      source,
      serverURL: serverURL.replace(/\/+$/, ''),
      token: env.EMBER_TOKEN || inputs.processToken || '',
      sourceColor: env.EMBER_SOURCE_COLOR ?? '',
      activityTrail: parseBool(env.EMBER_ACTIVITY_TRAIL_ENABLED, true),
      activityDetail: parseBool(env.EMBER_ACTIVITY_DETAIL_ENABLED, true),
      sourceCard: parseBool(env.EMBER_SOURCE_CARD, true),
      sessionBar: parseBool(env.EMBER_SESSION_BAR, true),
      sendProject: parseBool(env.EMBER_SEND_PROJECT, true),
      contextNumber: parseBool(env.EMBER_CONTEXT_NUMBER_ENABLED, false),
      rateBottomBar: parseBool(env.EMBER_RATE_BOTTOM_BAR, false),
      rateReset: parseBool(env.EMBER_RATE_RESET, false),
      doneTTLSeconds: positiveInt(env.EMBER_DONE_TTL_SECONDS, 30),
      heartbeatTTLHours: positiveInt(env.EMBER_HEARTBEAT_TTL_HOURS, 6),
      hookTimeoutMs: positiveInt(env.EMBER_HOOK_TIMEOUT_MS, 500),
      home: inputs.home,
    },
  }
}

export function truncate(s: string, n: number): string {
  const t = s.trim()
  const r = Array.from(t)
  return r.length <= n ? t : r.slice(0, n).join('')
}

function runeLen(s: string): number {
  return Array.from(s).length
}

function capTrail(s: string): string {
  if (runeLen(s) <= MAX_FIELD) return s
  let items = s.split(TRAIL_SEPARATOR)
  while (items.length > 1 && runeLen(items.join(TRAIL_SEPARATOR)) > MAX_FIELD) items = items.slice(0, -1)
  const out = items.join(TRAIL_SEPARATOR)
  return runeLen(out) > MAX_FIELD ? Array.from(out).slice(0, MAX_FIELD).join('') : out
}

export function prependTrail(head: string, prev: string): string {
  const h = head.trim()
  if (h === '') return prev
  if (prev === '') return capTrail(h)
  if (prev === h || prev.startsWith(h + TRAIL_SEPARATOR)) return prev
  return capTrail(h + TRAIL_SEPARATOR + prev)
}

export function annotateTrail(trail: string, item: string, annotated: string, prepend: boolean): string {
  const it = item.trim()
  const an = annotated.trim()
  if (it === '' || an === '') return trail
  if (trail !== '') {
    const items = trail.split(TRAIL_SEPARATOR)
    const i = items.indexOf(it)
    if (i >= 0) {
      items[i] = an
      return capTrail(items.join(TRAIL_SEPARATOR))
    }
  }
  return prepend ? prependTrail(an, trail) : trail
}

function field(input: unknown, key: string): string {
  if (input === null || typeof input !== 'object') return ''
  const v = (input as Record<string, unknown>)[key]
  return typeof v === 'string' ? v : ''
}

function baseName(p: string): string {
  const parts = p.split('/').filter(x => x !== '')
  return parts.length === 0 ? '' : (parts[parts.length - 1] ?? '')
}

export function activityString(tool: string, input: unknown): string {
  switch (tool) {
    case '':
      return ''
    case 'Bash': {
      const cmd = field(input, 'command')
      return cmd !== '' ? truncate('Bash: ' + cmd, MAX_FIELD) : 'Bash'
    }
    case 'Edit':
    case 'MultiEdit':
    case 'Write':
    case 'NotebookEdit':
    case 'Read': {
      const p = field(input, 'file_path') || field(input, 'notebook_path')
      return p !== '' ? truncate(tool + ': ' + baseName(p), MAX_FIELD) : tool
    }
    case 'Glob':
    case 'Grep': {
      const pat = field(input, 'pattern')
      return pat !== '' ? truncate(tool + ': ' + pat, MAX_FIELD) : tool
    }
  }
  return tool
}

export function annotatedActivity(activity: string, outcome: string): string {
  const suffix = ' (' + outcome + ')'
  return truncate(activity, MAX_FIELD - suffix.length) + suffix
}

export function failureOutcome(error: string, isInterrupt: boolean): string {
  if (isInterrupt) return 'aborted'
  const first = (error.split('\n')[0] ?? '').trim()
  const m = /^Exit code (\d{1,3})$/.exec(first)
  return m ? 'exit ' + m[1] : 'failed'
}

export function firstLine(s: string): string {
  for (const line of s.split('\n')) {
    const t = line.trim()
    if (t !== '') return t
  }
  return ''
}

const STOP_FAILURE_LABELS: Record<string, string> = {
  rate_limit: 'rate limited',
  overloaded: 'API overloaded',
  authentication_failed: 'auth failed',
  billing_error: 'billing error',
  server_error: 'API error',
  max_output_tokens: 'output limit',
  invalid_request: 'bad request',
  oauth_org_not_allowed: 'org not allowed',
  account_on_hold: 'account on hold',
  model_not_found: 'model not found',
  cloud_credential_error: 'cloud credentials',
}

export function stopFailureMessage(error: string, details: string, lastMessage: string): string {
  let label = STOP_FAILURE_LABELS[error] ?? ''
  if (label === '' && error !== '' && error !== 'unknown') label = error.replaceAll('_', ' ')
  const d = firstLine(details) || firstLine(lastMessage)
  if (label !== '' && d !== '') return label + ': ' + d
  return label || d || 'error'
}

export function hasWakingBackgroundTasks(tasks: unknown): boolean {
  if (!Array.isArray(tasks)) return false
  return tasks.some(t => ['subagent', 'workflow', 'teammate', 'cloud session'].includes(field(t, 'type')))
}

export function projectFromCwd(cwd: string, home: string): string {
  const c = cwd.trim().replace(/[/\\]+$/, '')
  if (c === '') return ''
  if (home !== '' && c === home.replace(/[/\\]+$/, '')) return ''
  const base = baseName(c)
  return base === '' || base === '.' ? '' : truncate(base, PROJECT_MAX)
}

export function validSessionID(raw: string): boolean {
  return raw !== '' && raw.length <= 64 && /^[A-Za-z0-9._-]+$/.test(raw)
}

export async function sessionID(raw: string, cwd: string): Promise<string> {
  if (validSessionID(raw)) return raw
  const sum = await crypto.subtle.digest('SHA-1', new TextEncoder().encode(cwd))
  return Array.from(new Uint8Array(sum), b => b.toString(16).padStart(2, '0'))
    .join('')
    .slice(0, 16)
}

export function stateHome(home: string, xdgStateHome: string): string {
  return xdgStateHome.startsWith('/') ? xdgStateHome : home + '/.local/state'
}

export function dialogExpected(interactive: boolean, permissionMode: string): boolean {
  return interactive && !['auto', 'dontAsk', 'bypassPermissions'].includes(permissionMode)
}

export type Session = {
  state: string
  message: string
  activity: string
  project: string
  changedAt: number
  hookAt: number
  pendingPermission: boolean
  pendingTool: string
  pendingToolUseId: string
  askedTool: string
  askedToolUseId: string
  lastTool: string
  lastToolUseId: string
  resumedTool: string
  resumedAt: number
  agentInput: boolean
  parked: boolean
}

type Upsert = {
  state: string
  message: string
  activity?: string
  cwd?: string
  pending?: { tool: string; toolUseId: string }
  agentInput?: boolean
  skip?: (prev: Session) => boolean
}

export class Tracker {
  readonly sessions = new Map<string, Session>()

  sessionStart(id: string, source: string): Effect[] {
    if (source !== 'startup') this.sessions.delete(id)
    return []
  }

  promptSubmit(cfg: Config, now: number, id: string, prompt: string, cwd: string): Effect[] {
    return this.upsert(cfg, now, id, { state: 'running', message: truncate(prompt, MAX_FIELD), cwd })
  }

  preToolUse(cfg: Config, now: number, id: string, tool: string, input: unknown, toolUseId: string, cwd: string): Effect[] {
    const out = this.upsert(cfg, now, id, {
      state: 'running',
      message: tool,
      activity: cfg.activityDetail ? activityString(tool, input) : '',
      cwd,
    })
    const s = this.sessions.get(id)
    if (s) {
      s.lastTool = tool
      s.lastToolUseId = toolUseId
    }
    return out
  }

  toolAsked(cfg: Config, now: number, id: string, tool: string, input: unknown, toolUseId: string, dialog: boolean, cwd: string): Effect[] {
    const out = dialog
      ? this.upsert(cfg, now, id, {
          state: 'waiting',
          message: 'approve ' + tool,
          activity: cfg.activityDetail ? activityString(tool, input) : '',
          cwd,
          pending: { tool, toolUseId },
        })
      : []
    const s = this.sessions.get(id)
    if (s) {
      s.askedTool = tool
      s.askedToolUseId = toolUseId
    }
    return out
  }

  permissionRequest(cfg: Config, now: number, id: string, tool: string, input: unknown, cwd: string): Effect[] {
    const s = this.sessions.get(id)
    let toolUseId = ''
    if (s && s.askedTool === tool) toolUseId = s.askedToolUseId
    else if (s && s.lastTool === tool) toolUseId = s.lastToolUseId
    if (s && s.state === 'waiting' && s.pendingPermission && s.pendingTool === tool && s.pendingToolUseId === toolUseId) return []
    return this.upsert(cfg, now, id, {
      state: 'waiting',
      message: 'approve ' + tool,
      activity: cfg.activityDetail ? activityString(tool, input) : '',
      cwd,
      pending: { tool, toolUseId },
    })
  }

  toolFinished(cfg: Config, now: number, id: string, tool: string, toolUseId: string): Effect[] {
    const s = this.sessions.get(id)
    if (!s || s.state !== 'waiting' || !s.pendingPermission) return []
    const sameCall = s.pendingToolUseId !== '' ? s.pendingToolUseId === toolUseId : s.pendingTool === tool
    if (!sameCall) return []
    s.state = 'running'
    s.message = truncate(tool, MAX_FIELD)
    s.pendingPermission = false
    s.pendingTool = ''
    s.pendingToolUseId = ''
    s.resumedTool = tool
    s.resumedAt = now
    s.changedAt = now
    s.hookAt = now
    return [this.post(cfg, id, s)]
  }

  toolOutcome(cfg: Config, id: string, tool: string, input: unknown, outcome: string): Effect[] {
    const s = this.sessions.get(id)
    if (!s || !cfg.activityDetail) return []
    const act = activityString(tool, input)
    if (act !== '') s.activity = annotateTrail(s.activity, act, annotatedActivity(act, outcome), cfg.activityTrail)
    return []
  }

  notification(cfg: Config, now: number, id: string, type: string, message: string, cwd: string): Effect[] {
    const up = (state: string, msg: string, x: Partial<Upsert> = {}) =>
      this.upsert(cfg, now, id, { state, message: msg, cwd, ...x })
    switch (type) {
      case 'permission_prompt':
        return up('waiting', message, {
          skip: prev =>
            prev.state !== 'waiting' &&
            prev.resumedAt !== 0 &&
            now - prev.resumedAt <= RESUME_GRACE_MS &&
            (message === '' || message.includes(prev.resumedTool)),
        })
      case 'agent_needs_input':
        return up('waiting', message, { agentInput: true })
      case 'elicitation_dialog':
      case 'elicitation_url_dialog':
      case 'quota_auto_resume_stale':
        return up('waiting', message)
      case 'elicitation_complete':
      case 'elicitation_response':
        return up('running', message, { skip: prev => prev.state !== 'waiting' || prev.pendingPermission })
      case 'quota_auto_resume_fired':
        return up('running', message || 'resumed')
      case 'quota_auto_resume_disabled':
        return up('error', message || 'usage limit')
      case 'agent_completed':
        return up('done', message)
      case 'idle_prompt':
        return up('done', message, { skip: prev => prev.state === 'done' || prev.state === 'error' })
    }
    return []
  }

  stop(cfg: Config, now: number, id: string, lastMessage: string, backgroundTasks: unknown, cwd: string): Effect[] {
    if (hasWakingBackgroundTasks(backgroundTasks)) return []
    return this.upsert(cfg, now, id, { state: 'done', message: firstLine(lastMessage), cwd })
  }

  stopFailure(cfg: Config, now: number, id: string, error: string, details: string, lastMessage: string, cwd: string): Effect[] {
    return this.upsert(cfg, now, id, { state: 'error', message: stopFailureMessage(error, details, lastMessage), cwd })
  }

  sessionEnd(cfg: Config, id: string, reason: string): Effect[] {
    if (!END_REASONS.includes(reason)) return []
    this.sessions.delete(id)
    return [{ method: 'DELETE', body: this.key(cfg, id) }]
  }

  tick(cfg: Config, now: number): Effect[] {
    const out: Effect[] = []
    for (const [id, s] of [...this.sessions]) {
      if (now - s.hookAt >= cfg.heartbeatTTLHours * 3_600_000) {
        this.sessions.delete(id)
        out.push({ method: 'DELETE', body: this.key(cfg, id) })
        continue
      }
      if (s.parked) continue
      if (s.state === 'waiting' && (!s.pendingPermission || s.agentInput) && now - Math.max(s.hookAt, s.changedAt) >= WAIT_BACKSTOP_MS) {
        s.state = 'idle'
        s.message = ''
        s.changedAt = now
        s.parked = true
        s.agentInput = false
        s.pendingPermission = false
        s.pendingTool = ''
        s.pendingToolUseId = ''
        out.push({ method: 'DELETE', body: this.key(cfg, id) })
        continue
      }
      const settled = s.state === 'done' || s.state === 'error'
      if (!settled || now - s.changedAt < cfg.doneTTLSeconds * 1000) out.push(this.post(cfg, id, s))
    }
    return out
  }

  private upsert(cfg: Config, now: number, id: string, u: Upsert): Effect[] {
    const prev = this.sessions.get(id)
    if (prev && u.skip && u.skip(prev)) return []
    const s: Session = prev ?? {
      state: '',
      message: '',
      activity: '',
      project: '',
      changedAt: now,
      hookAt: now,
      pendingPermission: false,
      pendingTool: '',
      pendingToolUseId: '',
      askedTool: '',
      askedToolUseId: '',
      lastTool: '',
      lastToolUseId: '',
      resumedTool: '',
      resumedAt: 0,
      agentInput: false,
      parked: false,
    }
    const activity = truncate(u.activity ?? '', MAX_FIELD)
    s.activity = cfg.activityTrail && activity !== '' && prev ? prependTrail(activity, prev.activity) : activity
    if (u.cwd !== undefined && u.cwd !== '') s.project = projectFromCwd(u.cwd, cfg.home)
    if (!prev || prev.state !== u.state) s.changedAt = now
    s.state = u.state
    s.message = truncate(u.message, MAX_FIELD)
    s.hookAt = now
    if (u.pending) {
      s.pendingPermission = true
      s.pendingTool = u.pending.tool
      s.pendingToolUseId = u.pending.toolUseId
      s.resumedTool = ''
      s.resumedAt = 0
    }
    if (u.state !== 'waiting') {
      s.pendingPermission = false
      s.pendingTool = ''
      s.pendingToolUseId = ''
    }
    s.agentInput = u.agentInput ?? false
    s.parked = false
    this.sessions.set(id, s)
    return [this.post(cfg, id, s)]
  }

  private key(cfg: Config, id: string): SessionKey {
    return { source: cfg.source, tool: 'claude', session: id }
  }

  private post(cfg: Config, id: string, s: Session): Effect {
    const body: Wire = { ...this.key(cfg, id), state: s.state, source_card: cfg.sourceCard, session_bar: cfg.sessionBar }
    if (s.message !== '') body.message = s.message
    if (cfg.sourceColor !== '') body.source_color = cfg.sourceColor
    if (s.activity !== '') body.activity = s.activity
    if (cfg.contextNumber) body.context_number = true
    if (cfg.rateBottomBar) body.rate_bottom_bar = true
    if (cfg.rateReset) body.rate_reset = true
    if (cfg.sendProject && s.project !== '') body.project = s.project
    return { method: 'POST', body }
  }
}

type Lane = { job: (() => Promise<unknown>) | undefined; drained: Promise<void> | undefined }

export class Outbox {
  private readonly lanes = new Map<string, Lane>()

  push(key: string, job: () => Promise<unknown>): Promise<void> {
    let lane = this.lanes.get(key)
    if (!lane) {
      lane = { job: undefined, drained: undefined }
      this.lanes.set(key, lane)
    }
    lane.job = job
    if (!lane.drained) lane.drained = this.drain(key, lane)
    return lane.drained
  }

  get busy(): number {
    return this.lanes.size
  }

  private async drain(key: string, lane: Lane): Promise<void> {
    for (let job = lane.job; job; job = lane.job) {
      lane.job = undefined
      try {
        await job()
      } catch {
        continue
      }
    }
    this.lanes.delete(key)
  }
}

export function bounded<T>(work: Promise<T>, timer: Promise<void>, onTimeout: T): Promise<T> {
  const expired = timer.then(
    () => onTimeout,
    () => new Promise<T>(() => {}),
  )
  return Promise.race([work, expired])
}

export async function send(fetch: FetchLike, cfg: Config, effect: Effect): Promise<boolean> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (cfg.token !== '') headers.Authorization = 'Bearer ' + cfg.token
  try {
    const res = await fetch(cfg.serverURL + '/v1/status', {
      method: effect.method,
      headers,
      body: JSON.stringify(effect.body),
    })
    return res.ok
  } catch {
    return false
  }
}
