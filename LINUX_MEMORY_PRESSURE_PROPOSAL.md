# Proposal: keep Cursor alive under Linux memory pressure

## Summary

Cursor Desktop can terminate its whole process tree when agent-created workloads
reach a cgroup v2 memory limit, even when the machine still has ample available
RAM. Cursor already logs that `CursorProclistService` has
`memoryPressureMonitorEnabled=true`, but the monitor does not shed disposable
workloads before the kernel invokes the cgroup OOM killer.

This proposal asks Cursor to:

1. account for aggregate memory across the editor, extension hosts, agent
   workers, terminals, builds, and browser automation;
2. isolate disposable agent workloads from the editor process tree;
3. shed or pause those workloads before a hard memory gate is reached; and
4. bound the growth of `state.vscdb`.

## Reproduction

Environment:

- Cursor 3.31.15 stable
- Linux 6.18.55, Wayland, cgroup v2, systemd 261
- 32 GiB RAM and 8 GiB zram
- one 15-folder multi-root workspace
- several Cursor windows / agent surfaces
- Angular development server and Playwright browser automation launched by an
  agent
- editor cgroup configured with `MemoryMax=16G` and `MemorySwapMax=4G`

Observed behavior:

1. Cursor starts 28 `NodeService` / extension-host processes.
2. Those processes consume about 6.9 GiB RSS by themselves.
3. A fresh session reaches 10.5 GiB in roughly two minutes:
   - extension/Node services: 6.9 GiB
   - Angular: 1.4 GiB
   - Cursor core/renderers: 1.2 GiB
   - Playwright/headless Chromium: 0.8 GiB
   - remaining agent and build children: about 1 GiB
4. Once the cgroup reaches the hard limit, the kernel kills a `headless_shell`
   or `ng serve` process.
5. Cursor then reports many extension hosts as killed and exits the whole
   application instead of preserving the editor.

Representative kernel event:

```text
oom-kill: constraint=CONSTRAINT_MEMCG,
  oom_memcg=.../app-cursor-<pid>.scope,
  task=headless_shell
Memory cgroup out of memory: Killed process ... (headless_shell)
```

Representative Cursor events:

```text
[CursorProclistService] Config enabled feature
  (subsample every 10s; memoryPressureMonitorEnabled=true)
[UtilityProcess type: extensionHost]: crashed with code 15 and reason 'killed'
```

The two exit signals identify a systemd scope-policy cascade:

- the kernel cgroup OOM killer terminates the selected workload
  (`headless_shell` or `ng serve`) with `SIGKILL` (9);
- the default `OOMPolicy=stop` for a systemd scope then terminates the remaining
  processes in that scope with `SIGTERM` (15), including Cursor extension
  hosts.

Setting `OOMPolicy=continue` on the scope prevents this secondary full-scope
shutdown. It does not replace workload isolation or memory-pressure handling:
the cgroup remains at its limit and may continue selecting processes until
memory usage falls.

Seven Cursor scopes were terminated by cgroup OOM during one day. The host
still had approximately 18 GiB available while one of the new sessions was
already using 10.5 GiB inside its own gate.

## Separate persistent-state issue

`~/.config/Cursor/User/globalStorage/state.vscdb` grew to approximately 74 GB.
It contained:

- 1,467 `ItemTable` rows;
- 3,862,229 `cursorDiskKV` rows;
- only 258 free SQLite pages.

This is live data growth, not space that `VACUUM` can substantially recover.
The database previously filled the disk and caused `SQLITE_FULL` / `ENOSPC`.
It also creates significant file-cache pressure.

Cursor should expose retention, compaction, and per-key-space diagnostics for
`cursorDiskKV`, with a safe automatic rebuild path when growth is abnormal.

## Suggested product design

### 1. Hierarchical workload isolation

On cgroup v2 systems, keep the editor and extension hosts in a protected group,
and put agent-created workloads in child or sibling groups:

```text
cursor-editor.scope
cursor-agent-build-<id>.scope
cursor-agent-browser-<id>.scope
cursor-agent-server-<id>.scope
```

Builds, tests, browser automation, and development servers should have
individual limits and lifecycles. Their OOM must not terminate the editor.

### 2. Act on the existing process monitor

Before the hard gate:

- at 75–80%: report aggregate usage and top consumers;
- at 85%: reclaim cache and stop idle browser automation;
- at 90%: pause or terminate disposable tests/builds;
- near the hard gate: preserve the main process, chats, and extension-host
  state, and surface a recovery notification.

Use cgroup `memory.current`, `memory.events`, `memory.reclaim`, PSI, and
`MemAvailable` rather than summing RSS alone.

### 3. Graceful cgroup OOM handling

If a child is killed by the cgroup OOM killer, Cursor should not shut down all
extension hosts. It should identify the affected agent command, mark that task
as failed, and keep the editor alive.

Scopes created by Cursor on Linux should explicitly use
`OOMPolicy=continue`. This prevents systemd from converting one kernel-selected
OOM victim into a `SIGTERM` shutdown of the whole scope. Separate scopes for
disposable workloads remain necessary so the kernel does not have to select
between a build, browser, extension host, renderer, and the editor itself.

### 4. Avoid extension-host multiplication

Review why one multi-root workspace plus several agent/window surfaces creates
28 extension hosts. Share safe built-in services where possible and expose a
per-window/per-extension memory breakdown in Process Explorer.

## Local mitigation validated

The following external mitigation kept the desktop responsive and avoided
immediate editor termination:

- no `MemoryHigh` throttle on the whole editor (it makes the UI stall);
- an 18 GiB hard editor limit plus 4 GiB swap;
- `OOMPolicy=continue` on editor and disposable-workload scopes, so one cgroup
  OOM victim does not cause systemd to stop the entire scope;
- a monitor that uses `memory.reclaim`, stops disposable browser/build
  children first, and only grants temporary headroom when system RAM is
  available;
- separate systemd scopes for builds/tests/servers;
- rebuilding the pathological state database after preserving a backup;
- restoring only one Cursor window and excluding generated directories from
  watchers/search.

This mitigation belongs in Cursor itself because an external watchdog cannot
reliably know which agent processes are safe to terminate.
