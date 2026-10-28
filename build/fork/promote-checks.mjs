// Authenticate a completed Release run before reusing its immutable artifacts.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const repository = 'zwenooo/magpie';
export const requiredJobs = [
  'prepare', 'quality / Workflow and publishing contracts',
  ...['ubuntu-latest', 'macos-latest', 'windows-latest'].map(os => `quality / test (${os})`),
  ...['linux/amd64', 'android/arm64'].map(target => `quality / cli (${target}, nogui)`),
  ...['ubuntu-latest', 'macos-latest', 'windows-latest'].map(os => `fork-checks / fork (${os})`),
  ...[
    'windows-latest, windows, amd64', 'windows-11-arm, windows, arm64',
    'macos-latest, darwin, arm64', 'macos-15-intel, darwin, amd64',
    'ubuntu-22.04, linux, amd64', 'ubuntu-22.04-arm, linux, arm64',
  ].map(target => `cursor-backend (${target})`),
  'cli', 'mac (macos-latest, arm64)', 'mac (macos-15-intel, amd64)',
  'linux (ubuntu-22.04, amd64)', 'linux (ubuntu-22.04-arm, arm64)',
  'windows-check (windows-latest, amd64)', 'windows-check (windows-11-arm, arm64)', 'package',
];

export function parseTag(tag) {
  const match = /^va\.((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))-vb\.((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$/.exec(tag);
  assert(match, 'Expected a va.X.Y.Z-vb.X.Y.Z public release tag');
  return { tag, version: `${match[1]}-vb.${match[2]}`, official: match[1], fork: match[2] };
}

export function validateRun(run, workflow, runID) {
  assert(/^[1-9][0-9]*$/.test(runID) && Number.isSafeInteger(Number(runID)), 'Invalid run ID');
  assert.equal(String(run.id), runID, 'Run ID mismatch');
  assert.equal(run.repository?.full_name, repository, 'Wrong source repository');
  assert.equal(run.head_repository?.full_name, repository, 'Fork-supplied run is not trusted');
  assert.equal(run.head_repository.id, run.repository.id, 'Source repository ID mismatch');
  assert.equal(workflow.path, '.github/workflows/release.yml', 'Wrong workflow path');
  assert.equal(run.path, workflow.path, 'Source is not the Release workflow');
  assert(Number.isSafeInteger(workflow.id), 'Missing workflow identity');
  assert.equal(run.workflow_id, workflow.id, 'Wrong workflow ID');
  assert.equal(run.head_branch, 'main', 'Source build must run on main');
  assert(['push', 'workflow_dispatch'].includes(run.event), 'Untrusted source event');
  assert.equal(run.status, 'completed', 'Source run has not completed');
  assert.equal(run.conclusion, 'success', 'Source run did not pass');
  assert(/^[0-9a-f]{40}$/.test(run.head_sha), 'Invalid source SHA');
  assert(Number.isSafeInteger(run.run_attempt) && run.run_attempt > 0, 'Invalid run attempt');
}

export function validateJobs(run, jobs) {
  const byName = new Map();
  for (const job of jobs) {
    assert(!byName.has(job.name), `Duplicate job: ${job.name}`);
    assert.equal(job.run_id, run.id, 'Job belongs to another run');
    assert.equal(job.run_attempt, run.run_attempt, 'Job belongs to another attempt');
    assert.equal(job.head_sha, run.head_sha, 'Job source SHA mismatch');
    assert.equal(job.status, 'completed', `Unfinished job: ${job.name}`);
    const skippedPublish = ['publish', 'homebrew'].includes(job.name) && job.conclusion === 'skipped';
    assert(job.conclusion === 'success' || skippedPublish, `Job did not pass: ${job.name}`);
    byName.set(job.name, job);
  }
  for (const name of requiredJobs) {
    assert.equal(byName.get(name)?.conclusion, 'success', `Required check did not pass: ${name}`);
  }
  return byName.get('prepare').id;
}

export function validateArtifacts(run, artifacts, now = Date.now()) {
  return Object.fromEntries(['release-bundle', 'release-notes'].map(name => {
    const matches = artifacts.filter(artifact => artifact.name === name);
    assert.equal(matches.length, 1, `Expected one ${name} artifact`);
    const artifact = matches[0];
    assert(Number.isSafeInteger(artifact.id) && artifact.id > 0, 'Invalid artifact ID');
    assert.equal(artifact.expired, false, `${name} has expired`);
    assert(Date.parse(artifact.expires_at) > now && artifact.size_in_bytes > 0, `${name} is unavailable`);
    assert.equal(artifact.workflow_run?.id, run.id, 'Artifact belongs to another run');
    assert.equal(artifact.workflow_run.head_sha, run.head_sha, 'Artifact source SHA mismatch');
    assert.equal(artifact.workflow_run.repository_id, run.repository.id, 'Artifact repository mismatch');
    assert.equal(artifact.workflow_run.head_repository_id, run.repository.id, 'Artifact originated in a fork');
    return [name, artifact.id];
  }));
}

export function validatePrepareLog(log, sha, release) {
  const lines = log.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '').split(/\r?\n/)
    .map(line => line.replace(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z /, ''));
  const starts = lines.flatMap((line, index) => line.startsWith('Source: ') ? [index] : []);
  assert.equal(starts.length, 1, 'Missing or ambiguous prepare metadata');
  assert.deepEqual(lines.slice(starts[0], starts[0] + 6), [
    `Source: ${sha}`, `Version: ${release.version}`, `Release tag: ${release.tag}`,
    `Official base: ${release.official}`, `Fork revision: ${release.fork}`, 'Publish: false',
  ], 'Prepared source/version does not match the requested build-only release');
}

async function responseText(response) {
  assert(response.ok, `GitHub request failed: HTTP ${response.status}`);
  const chunks = [];
  let length = 0;
  for await (const chunk of response.body) {
    length += chunk.length;
    assert(length <= 4 * 1024 * 1024, 'GitHub metadata/log exceeds its size limit');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

async function inspect(runID, tag) {
  const release = parseTag(tag);
  assert(/^[1-9][0-9]*$/.test(runID), 'Invalid run ID');
  assert.equal(process.env.GITHUB_REPOSITORY, repository, 'Wrong promotion repository');
  assert.equal(process.env.GITHUB_REF, 'refs/heads/main', 'Promotion must use the main workflow');
  assert(process.env.GH_TOKEN, 'GitHub token is required');
  const request = endpoint => fetch(`https://api.github.com/repos/${repository}/${endpoint}`, {
    headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' },
    redirect: 'manual', signal: AbortSignal.timeout(30_000),
  });
  const json = async endpoint => JSON.parse(await responseText(await request(endpoint)));
  const [run, workflow] = await Promise.all([json(`actions/runs/${runID}`), json('actions/workflows/release.yml')]);
  validateRun(run, workflow, runID);
  const pages = async (endpoint, field) => {
    const all = [];
    for (let page = 1; page <= 10; page++) {
      const result = await json(`${endpoint}?per_page=100&page=${page}`);
      assert(Array.isArray(result[field]), `Missing ${field}`);
      all.push(...result[field]);
      if (all.length === result.total_count) return all;
      assert(result[field].length > 0, `Incomplete ${field} pagination`);
    }
    throw new Error(`Too many ${field} pages`);
  };
  const [jobs, artifacts] = await Promise.all([
    pages(`actions/runs/${runID}/attempts/${run.run_attempt}/jobs`, 'jobs'),
    pages(`actions/runs/${runID}/artifacts`, 'artifacts'),
  ]);
  const prepareID = validateJobs(run, jobs);
  const ids = validateArtifacts(run, artifacts);
  let logs = await request(`actions/jobs/${prepareID}/logs`);
  if (logs.status === 302) {
    const target = new URL(logs.headers.get('location'));
    assert(target.protocol === 'https:' && !target.username && !target.password, 'Invalid log download URL');
    // GitHub redirects to signed blob storage; never forward the API credential.
    logs = await fetch(target, { signal: AbortSignal.timeout(30_000) });
  }
  validatePrepareLog(await responseText(logs), run.head_sha, release);
  const output = { source_sha: run.head_sha, version: release.version, tag, official: release.official,
    bundle_id: ids['release-bundle'], notes_id: ids['release-notes'] };
  fs.appendFileSync(process.env.GITHUB_OUTPUT, Object.entries(output).map(([key, value]) => `${key}=${value}\n`).join(''));
  console.log(`Verified Release run ${runID}, ${requiredJobs.length} required jobs, ${run.head_sha}, ${tag}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  inspect(process.argv[2], process.argv[3]).catch(error => { console.error(error.message); process.exitCode = 1; });
}
