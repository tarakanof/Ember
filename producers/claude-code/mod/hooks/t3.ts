export type T3Verdict = 'yes' | 'no' | 'unknown'

export type ProcInfo = { ppid: number; comm: string }

export type T3Probe = {
  isSDK: () => Promise<boolean>
  start: () => Promise<number | undefined>
  info: (pid: number) => Promise<ProcInfo | undefined>
  argv: (pid: number) => Promise<string[] | undefined>
}

export const T3_PROBE_BUDGET_MS = 250
export const T3_RETRY_MS = 30_000

const SHELLS = new Set(['sh', 'bash', 'zsh', 'dash', 'fish', 'ksh', 'csh', 'tcsh', 'login', 'env'])
const MAX_WALK = 12

function baseName(p: string): string {
  const parts = p.split('/').filter(x => x !== '')
  return parts.length === 0 ? '' : (parts[parts.length - 1] ?? '')
}

export function isT3Executable(path: string): boolean {
  return baseName(path) === 't3'
}

export function isT3ServerArgv(argv: string[]): boolean {
  const first = argv[0]
  if (first === undefined) return false
  if (isT3Executable(first)) return true
  for (let i = 1; i + 1 < argv.length; i++) {
    if ((argv[i] ?? '').endsWith('/app.asar/apps/server/dist/bin.mjs') && argv[i + 1] === '--bootstrap-fd') return true
  }
  const runtime = baseName(first)
  if (runtime === 'node' || runtime === 'bun') {
    for (const a of argv.slice(1)) {
      if (a.startsWith('-')) continue
      return isT3Executable(a) || a.endsWith('/node_modules/t3/dist/bin.mjs')
    }
  }
  return false
}

export function argvFromPs(comm: string, args: string): string[] {
  const line = args.trim()
  const exe = comm.trim()
  if (exe !== '' && line.startsWith(exe)) {
    const rest = line.slice(exe.length)
    if (rest === '' || /^\s/.test(rest)) return [exe, ...rest.split(/\s+/).filter(x => x !== '')]
  }
  return line.split(/\s+/).filter(x => x !== '')
}

export function parsePsInfo(out: string): ProcInfo | undefined {
  const fields = out.split(/\s+/).filter(x => x !== '')
  const ppid = fields[0] ?? ''
  if (fields.length < 2 || !/^\d+$/.test(ppid)) return undefined
  return { ppid: Number(ppid), comm: fields.slice(1).join(' ') }
}

export function parseProcStat(text: string): ProcInfo | undefined {
  const open = text.indexOf('(')
  const close = text.lastIndexOf(')')
  if (open < 0 || close < open) return undefined
  const rest = text.slice(close + 1).trim().split(/\s+/)
  const ppid = rest[1] ?? ''
  if (!/^\d+$/.test(ppid)) return undefined
  return { ppid: Number(ppid), comm: text.slice(open + 1, close) }
}

export function argvFromCmdline(text: string): string[] | undefined {
  const argv = text.split('\0')
  if (argv[argv.length - 1] === '') argv.pop()
  return argv.length === 0 || argv[0] === '' ? undefined : argv
}

async function walkPastShells(start: number, info: T3Probe['info']): Promise<{ pid: number; ppid: number } | undefined> {
  let cur = start
  for (let i = 0; i < MAX_WALK && cur > 1; i++) {
    const p = await info(cur)
    if (!p) return undefined
    if (!SHELLS.has(baseName(p.comm.replace(/^-/, '')))) return { pid: cur, ppid: p.ppid }
    cur = p.ppid
  }
  return undefined
}

export async function detectT3(probe: T3Probe): Promise<T3Verdict> {
  if (!(await probe.isSDK())) return 'no'
  const start = await probe.start()
  if (start === undefined) return 'unknown'
  const owner = await walkPastShells(start, probe.info)
  if (!owner) return 'unknown'
  if (owner.ppid <= 1) return 'no'
  const launcher = await walkPastShells(owner.ppid, probe.info)
  if (!launcher) return 'unknown'
  const argv = await probe.argv(launcher.pid)
  if (!argv) return 'unknown'
  return isT3ServerArgv(argv) ? 'yes' : 'no'
}
