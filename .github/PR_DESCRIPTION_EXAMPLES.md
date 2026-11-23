# Pull request description examples

Right-sized descriptions for `.github/PULL_REQUEST_TEMPLATE.md`. Each stays within the template's word budget (150 for a bug fix, 300 for a feature), and a reviewer gets the point in under a minute. The checklist is left out to keep them short.

## Bug fix

The symptom users saw, the root cause in two sentences, the fix, and a test that fails on master.

~~~markdown
fix(localmodelnode): start downloads on tainted nodes

**What this PR does / why we need it**:

On a tainted GPU node a cache stays at `NodeDownloadPending` forever, without an error.

The node agent tolerates the taint through the chart's `tolerations`, but the download Jobs it creates do not, so the scheduler never places them. The Jobs now get the same tolerations.

**Feature/Issue validation/testing**:

A chart test sets a toleration and expects it on both. On a cluster with a tainted node the download now completes. Both fail on master.

**Release note**:

```release-note
`localmodelcache.localmodelnode.controller.tolerations` now applies to download Jobs as well as to the node agent.
```
~~~

## Feature

What users can do now and why they need it, a short example, then the decisions a reviewer should weigh in on and whether an upgrade changes anything.

~~~markdown
feat(localmodel): refuse caches that exceed the storage limit

**What this PR does / why we need it**:

`storageLimit` on a LocalModelNodeGroup was only documentation. A cache too large for the disks of the group was accepted, and its downloads failed on every node once the disk was full.

A cache is now refused when the `modelSize` of all caches in a group would exceed its limit:

```
LocalModelNodeGroup gpu: caching hf://org/model needs 210Gi in total, which exceeds its storageLimit 200Gi
```

Caches with the same source URI share one folder on each node, so they count once.

**Feature/Issue validation/testing**:

Table tests cover create, update, shared URIs and several groups. On OpenShift, a cache over the limit was refused and one that fits exactly was accepted.

**Special notes for your reviewer**:

- The check trusts `modelSize`. Measuring the download would be exact, but only known once the disk is in use.
- Two caches created at the same moment can both pass. The alternative, a lock, would hold up every create.

Caches that already exceed a limit keep working and can be updated. Nothing rolls on upgrade.

**Release note**:

```release-note
Creating or growing a cache fails when the `modelSize` of all caches in a LocalModelNodeGroup would exceed its `storageLimit`.
```
~~~

## Refactor

Why the refactor is worth it, one number, and a plain statement that upgrades roll nothing. The only user-visible change gets one sentence.

~~~markdown
refactor(localmodel): share the download container between both kinds of Job

**What this PR does / why we need it**:

Per-node download Jobs and shared-PVC import Jobs each built their own download container. Settings had to be added twice, and the CA bundle reached only one of them. Both now use `pkg/controller/v1alpha1/localmodel/jobs`, which removes about 120 lines of duplication.

Download Jobs are unchanged. Import Jobs now mount the CA bundle too, which is the only change users can see.

**Special notes for your reviewer**:

The helpers take the settings as arguments instead of reading the ConfigMap, so their callers decide how often it is read.

**Release note**:

```release-note
NONE
```
~~~
