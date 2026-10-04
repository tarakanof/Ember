#!/usr/bin/env python3
"""proc_pid_rusage (RUSAGE_INFO_V4) sampler, no sudo.
usage: rusage.py snap PID... -> JSON per pid (cumulative counters)
       rusage.py window SECONDS LABEL=PID[,PID...] ...  -> deltas over window, plus descendants
"""
import ctypes, ctypes.util, json, subprocess, sys, time
lib = ctypes.CDLL('/usr/lib/libproc.dylib')
F = ['user_time','system_time','pkg_idle_wkups','interrupt_wkups','pageins','wired_size','resident_size','phys_footprint',
     'proc_start_abstime','proc_exit_abstime','child_user_time','child_system_time','child_pkg_idle_wkups','child_interrupt_wkups',
     'child_pageins','child_elapsed_abstime','diskio_bytesread','diskio_byteswritten',
     'qos_default','qos_maint','qos_bg','qos_util','qos_legacy','qos_ui','qos_ui2','billed_system_time','serviced_system_time',
     'logical_writes','lifetime_max_phys_footprint','instructions','cycles','billed_energy','serviced_energy','interval_max_phys_footprint','runnable_time']
class RU(ctypes.Structure):
    _fields_ = [('uuid', ctypes.c_uint8*16)] + [(f, ctypes.c_uint64) for f in F] + [('pad', ctypes.c_uint64*16)]
class TB(ctypes.Structure):
    _fields_ = [('numer', ctypes.c_uint32), ('denom', ctypes.c_uint32)]
tb = TB(); ctypes.CDLL(None).mach_timebase_info(ctypes.byref(tb))
def ns(t): return t * tb.numer / tb.denom
def snap(pid):
    r = RU()
    if lib.proc_pid_rusage(int(pid), 4, ctypes.byref(r)) != 0: return None
    return {f: getattr(r, f) for f in F}
def descendants(pids):
    out = subprocess.run(['ps','-axo','pid=,ppid='],capture_output=True,text=True).stdout.split('\n')
    kids = {}
    for l in out:
        p = l.split()
        if len(p)==2: kids.setdefault(int(p[1]),[]).append(int(p[0]))
    res, st = [], [int(p) for p in pids]
    while st:
        p = st.pop(); res.append(p); st += kids.get(p, [])
    return res
if __name__ == '__main__':
    if sys.argv[1] == 'snap':
        print(json.dumps({p: snap(p) for p in sys.argv[2:]}))
    else:
        secs = float(sys.argv[2]); groups = {}
        for a in sys.argv[3:]:
            k, v = a.split('='); groups[k] = v.split(',')
        t0 = time.time(); s0 = {k: {p: snap(p) for p in descendants(v)} for k, v in groups.items()}
        time.sleep(secs)
        dt = time.time() - t0; res = {}
        for k, v in groups.items():
            pids = descendants(v); acc = dict(cpu_s=0.0, wkups=0, disk_w=0, logical_w=0, disk_r=0, rss=0, footprint=0, procs=len(pids))
            for p in pids:
                b = snap(p); a = s0[k].get(p) or {f: 0 for f in F}
                if not b: continue
                acc['cpu_s'] += ns((b['user_time']-a['user_time'])+(b['system_time']-a['system_time']))/1e9
                acc['wkups'] += (b['pkg_idle_wkups']-a['pkg_idle_wkups'])+(b['interrupt_wkups']-a['interrupt_wkups'])
                acc['disk_w'] += b['diskio_byteswritten']-a['diskio_byteswritten']
                acc['disk_r'] += b['diskio_bytesread']-a['diskio_bytesread']
                acc['logical_w'] += b['logical_writes']-a['logical_writes']
                acc['rss'] += b['resident_size']; acc['footprint'] += b['phys_footprint']
            res[k] = dict(window_s=round(dt,1), procs=acc['procs'], cpu_pct=round(100*acc['cpu_s']/dt,3),
                          wakeups_per_s=round(acc['wkups']/dt,2), disk_w_bytes=acc['disk_w'], disk_w_per_h=round(acc['disk_w']*3600/dt),
                          logical_w_per_h=round(acc['logical_w']*3600/dt), disk_r_bytes=acc['disk_r'],
                          rss_mb=round(acc['rss']/2**20,1), footprint_mb=round(acc['footprint']/2**20,1),
                          gb_per_year=round(acc['disk_w']*3600*24*365/dt/1e9,2))
        print(json.dumps(res, indent=1))
