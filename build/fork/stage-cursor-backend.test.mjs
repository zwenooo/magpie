import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { stageBackend } from './stage-cursor-backend.mjs';

const revision = '0123456789abcdef0123456789abcdef01234567';
function fixture(t, platform = 'windows', arch = 'amd64') {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'magpie-backend-stage-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const input = path.join(root, 'input'), output = path.join(root, 'output');
  fs.mkdirSync(input);
  fs.mkdirSync(output);
  const binary = Buffer.alloc(128);
  if (platform === 'windows') {
    binary.write('MZ'); binary.writeUInt32LE(64, 60); binary.write('PE\0\0', 64);
    binary.writeUInt16LE(arch === 'amd64' ? 0x8664 : 0xaa64, 68);
  } else if (platform === 'linux') {
    Buffer.from('7f454c460201', 'hex').copy(binary);
    binary.writeUInt16LE(arch === 'amd64' ? 62 : 183, 18);
  } else {
    binary.writeUInt32LE(0xfeedfacf); binary.writeUInt32LE(arch === 'amd64' ? 0x01000007 : 0x0100000c, 4);
  }
  const name = `magpie-cursor-backend${platform === 'windows' ? '.exe' : ''}`;
  fs.writeFileSync(path.join(input, name), binary);
  const manifest = { schema: 1, source: revision, os: platform, arch, file: name, sha256: createHash('sha256').update(binary).digest('hex') };
  const save = () => fs.writeFileSync(path.join(input, 'backend.json'), JSON.stringify(manifest));
  save();
  return { input, output, binary, name, manifest, save };
}

for (const platform of ['windows', 'darwin', 'linux']) {
  for (const arch of ['amd64', 'arm64']) {
    test(`stages verified ${platform}/${arch} executable`, t => {
      const f = fixture(t, platform, arch);
      stageBackend(f.input, platform, arch, revision, f.output);
      assert.deepEqual(fs.readFileSync(path.join(f.output, f.name)), f.binary);
    });
  }
}

test('rejects another commit or architecture before replacing the payload', t => {
  const f = fixture(t);
  fs.writeFileSync(path.join(f.output, f.name), 'existing');
  assert.throws(() => stageBackend(f.input, 'windows', 'arm64', revision, f.output), /provenance/);
  assert.throws(() => stageBackend(f.input, 'windows', 'amd64', 'other', f.output), /provenance/);
  assert.equal(fs.readFileSync(path.join(f.output, f.name), 'utf8'), 'existing');
});

test('rejects changed bytes and a falsely labeled executable', t => {
  const f = fixture(t);
  fs.appendFileSync(path.join(f.input, f.name), 'corrupt');
  assert.throws(() => stageBackend(f.input, 'windows', 'amd64', revision, f.output), /checksum/);
  fs.writeFileSync(path.join(f.input, f.name), f.binary);
  f.manifest.arch = 'arm64'; f.save();
  assert.throws(() => stageBackend(f.input, 'windows', 'arm64', revision, f.output), /executable/);
});

test('Android omits the desktop helper and cannot retain a previous target', t => {
  const f = fixture(t);
  stageBackend(f.input, 'windows', 'amd64', revision, f.output);
  fs.writeFileSync(path.join(f.output, 'magpie-cursor-backend'), 'previous unix backend');
  stageBackend(f.input, 'android', 'arm64', revision, f.output);
  assert.equal(fs.existsSync(path.join(f.output, f.name)), false);
  assert.equal(fs.existsSync(path.join(f.output, 'magpie-cursor-backend')), false);
});
