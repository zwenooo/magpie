import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseTag, requiredJobs, validateRun, validateJobs, validateArtifacts, validatePrepareLog } from './promote-checks.mjs';

const sha = 'a'.repeat(40), runID = '40656942852';
const release = parseTag('va.0.1.1725-vb.0.0.5');
const workflow = { id: 398023016, path: '.github/workflows/release.yml' };
const run = { id: Number(runID), repository: { id: 123, full_name: 'zwenooo/magpie' }, head_repository: { id: 123, full_name: 'zwenooo/magpie' },
  workflow_id: workflow.id, path: workflow.path, event: 'workflow_dispatch', head_branch: 'main', head_sha: sha,
  run_attempt: 1, status: 'completed', conclusion: 'success' };
const jobs = requiredJobs.map((name, index) => ({ id: index + 1, name, run_id: run.id, run_attempt: 1, head_sha: sha, status: 'completed', conclusion: 'success' }));
const artifacts = ['release-bundle', 'release-notes'].map((name, index) => ({ id: index + 100, name, expired: false, size_in_bytes: 12,
  expires_at: '2099-01-01T00:00:00Z', workflow_run: { id: run.id, head_sha: sha, repository_id: 123, head_repository_id: 123 } }));
const lines = [`Source: ${sha}`, `Version: ${release.version}`, `Release tag: ${release.tag}`, `Official base: ${release.official}`, `Fork revision: ${release.fork}`, 'Publish: false'];
const log = ['Version: 20261021.618', ...lines].map(line => `2026-10-28T08:29:32.8264811Z ${line}`).join('\n');

test('accepts only a completed successful own Release run', () => {
  validateRun(run, workflow, runID);
  for (const patch of [
    { status: 'in_progress' }, { conclusion: 'failure' }, { event: 'pull_request' },
    { head_branch: 'feat/test' }, { head_sha: 'abc' }, { workflow_id: 999 },
    { path: '.github/workflows/other.yml' }, { head_repository: { id: 999, full_name: 'other/magpie' } },
  ]) assert.throws(() => validateRun({ ...run, ...patch }, workflow, runID));
  assert.throws(() => validateRun(run, workflow, '1\nsource_sha=x'));
});

test('all 24 required jobs must pass; only publication jobs may be skipped', () => {
  assert.equal(requiredJobs.length, 24);
  assert.equal(validateJobs(run, jobs), 1);
  validateJobs(run, [...jobs, { ...jobs[0], name: 'publish', conclusion: 'skipped' }]);
  assert.throws(() => validateJobs(run, jobs.slice(1)));
  assert.throws(() => validateJobs(run, [...jobs, jobs[0]]));
  for (const patch of [{ conclusion: 'skipped' }, { conclusion: 'failure' }, { run_attempt: 2 }, { head_sha: 'b'.repeat(40) }]) {
    assert.throws(() => validateJobs(run, [{ ...jobs[0], ...patch }, ...jobs.slice(1)]));
  }
});

test('selects unique live artifacts from the exact run and source', () => {
  assert.deepEqual(validateArtifacts(run, artifacts), { 'release-bundle': 100, 'release-notes': 101 });
  assert.throws(() => validateArtifacts(run, artifacts.slice(1)));
  assert.throws(() => validateArtifacts(run, [...artifacts, artifacts[0]]));
  for (const patch of [{ expired: true }, { expires_at: '2000-01-01T00:00:00Z' }, { size_in_bytes: 0 },
    { workflow_run: { ...artifacts[0].workflow_run, id: 1 } }, { workflow_run: { ...artifacts[0].workflow_run, head_sha: 'b'.repeat(40) } }]) {
    assert.throws(() => validateArtifacts(run, [{ ...artifacts[0], ...patch }, artifacts[1]]));
  }
});

test('prepared version is an exact contiguous metadata record, not a runner version', () => {
  validatePrepareLog(log, sha, release);
  validatePrepareLog(log.replace('Source:', '\x1b[32mSource:'), sha, release);
  for (const changed of [log.replace(release.version, 'fork-aaaaaaaa'), log.replace('Publish: false', 'Publish: true'),
    log.replace(sha, 'b'.repeat(40)), log + '\nSource: duplicate', log.replace('Official base:', 'unexpected\nOfficial base:')]) {
    assert.throws(() => validatePrepareLog(changed, sha, release));
  }
  for (const tag of ['v0.1.1725', 'va.00.1.1725-vb.0.0.5', 'va.0.1.1725-vb.0.0.5\n', 'va.0.1.1725-vb.0.0.5+foo']) {
    assert.throws(() => parseTag(tag));
  }
});
