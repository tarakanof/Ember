export type EmberModSession = {
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
  contextPct: number
  ratePct: number
  rateResetAt: number
  rateResetLabel: string
  gaugesAsked: boolean
}

export type EmberModSnapshot = {
  v: 1
  sessions: [string, EmberModSession][]
  strays: [string, number[]][]
  permissionMode: string
  t3: 'yes' | 'no' | ''
}

declare module 'claude-code' {
  interface PluginState {
    'ember-mod': { snapshot: EmberModSnapshot }
  }
}
