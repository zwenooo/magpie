// Stage one verified native backend before Go embeds it. A cross-build must
// never silently package the previous architecture's helper.
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';

export function stageBackend(input, os, arch, source, output) {
  const names = ['magpie-cursor-backend', 'magpie-cursor-backend.exe'];
  const filename = os === 'windows' ? names[1] : names[0];
  if (os === 'android' && arch === 'arm64') {
    fs.mkdirSync(output, { recursive: true });
    for (const name of names) fs.rmSync(path.join(output, name), { force: true });
    return;
  }
  if (!['windows', 'darwin', 'linux'].includes(os) || !['amd64', 'arm64'].includes(arch)) {
    throw new Error(`Unsupported Cursor backend target: ${os}/${arch}`);
  }
  const manifest = JSON.parse(fs.readFileSync(path.join(input, 'backend.json'), 'utf8'));
  if (manifest.schema !== 1 || manifest.os !== os || manifest.arch !== arch || manifest.source !== source || manifest.file !== filename) {
    throw new Error(`Cursor backend provenance does not match ${os}/${arch} at ${source}`);
  }
  const binary = fs.readFileSync(path.join(input, filename));
  if (createHash('sha256').update(binary).digest('hex') !== manifest.sha256) {
    throw new Error('Cursor backend checksum mismatch');
  }
  if (!matchesTarget(binary, os, arch)) throw new Error(`Cursor backend executable is not ${os}/${arch}`);
  fs.mkdirSync(output, { recursive: true });
  // Validate before changing the active payload, so a bad artifact is harmless.
  fs.writeFileSync(path.join(output, filename), binary, { mode: 0o755 });
  fs.chmodSync(path.join(output, filename), 0o755);
  for (const name of names) {
    if (name !== filename) fs.rmSync(path.join(output, name), { force: true });
  }
}

function matchesTarget(binary, os, arch) {
  if (binary.length < 64) return false;
  if (os === 'windows') {
    if (binary.toString('ascii', 0, 2) !== 'MZ') return false;
    const offset = binary.readUInt32LE(60);
    return offset <= binary.length - 6 && binary.toString('hex', offset, offset + 4) === '50450000' &&
      binary.readUInt16LE(offset + 4) === (arch === 'amd64' ? 0x8664 : 0xaa64);
  }
  if (os === 'linux') {
    return binary.toString('hex', 0, 6) === '7f454c460201' &&
      binary.readUInt16LE(18) === (arch === 'amd64' ? 62 : 183);
  }
  return binary.readUInt32LE(0) === 0xfeedfacf &&
    binary.readUInt32LE(4) === (arch === 'amd64' ? 0x01000007 : 0x0100000c);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [input, os, arch, source, output = path.resolve(import.meta.dirname, '../../internal/cursorbyok/payload')] = process.argv.slice(2);
  if (!input || !os || !arch || !source) throw new Error('usage: stage-cursor-backend.mjs INPUT OS ARCH SOURCE_SHA [OUTPUT]');
  stageBackend(input, os, arch, source, output);
}
