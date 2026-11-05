# Qualification collector

The host collector defends against accidental mislabelling on the runners and
hosts we dispatch: wrong VM size, containers, inherited cgroup limits, and
missing or unreadable files. It does not defend against a deliberately hostile
environment that fakes procfs. Deliberate procfs forgery is out of scope.

The collector requires `/proc` and `/proc/1` to belong to the same procfs
instance: both opened directory descriptors must report `PROC_SUPER_MAGIC`
through statfs and the same `st_dev` through stat. It also requires readable
`/proc/self/ns/mnt` and `/proc/1/ns/mnt` descriptors. Failure of either structural
check refuses the profile. These checks do not authenticate a deliberately
forged procfs view; no further anti-forgery machinery is provided.

Namespace, hardware, affinity, cgroup ancestry and effective-limit checks remain
required. Unknown or unreadable required evidence fails rather than becoming
an unlimited resource claim. Receipts retain raw evidence and errors, including
namespace/procfs descriptor facts before and after collection.

The dispatch-only host workflow checks this evidence protocol. It opens no
database and supplies no engine, latency, service, memory-capacity or workload
qualification by itself.
