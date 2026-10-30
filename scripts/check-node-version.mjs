#!/usr/bin/env node
// One Node release builds the web app everywhere: CI, the release workflow and the container
// image. `.node-version` names it; every workflow reads that file, and the Dockerfile's web stage
// names the same release in its (digest-pinned) image tag. This fails when they drift apart, or
// when it runs under a different Node (CI runs it right after setting Node up).
import {readFileSync, readdirSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const wanted = readFileSync(path.join(root, '.node-version'), 'utf8').trim();
const problems = [];
if (!/^\d+\.\d+\.\d+$/.test(wanted)) problems.push(`.node-version must name one exact release, not "${wanted}"`);

const docker = readFileSync(path.join(root, 'Dockerfile'), 'utf8');
const image = docker.match(/^FROM node:([^\s@]+)@sha256:[0-9a-f]{64} AS web$/m);
if (!image) problems.push('Dockerfile: the web stage must use a digest-pinned node image');
else if (!image[1].startsWith(wanted + '-')) problems.push(`Dockerfile: the web stage uses node ${image[1]}, .node-version says ${wanted}`);

const workflows = path.join(root, '.github/workflows');
for (const name of readdirSync(workflows).filter(f => /\.ya?ml$/.test(f))) {
  const text = readFileSync(path.join(workflows, name), 'utf8');
  for (const m of text.matchAll(/node-version:\s*([^\s,}]+)/g)) problems.push(`${name}: sets node-version ${m[1]}; use node-version-file: .node-version`);
}

if (process.env.CI && process.version !== 'v' + wanted) problems.push(`running under Node ${process.version}, .node-version says ${wanted}`);

if (problems.length) {
  for (const p of problems) console.error('check-node-version: ' + p);
  process.exit(1);
}
console.log(`check-node-version: Node ${wanted} everywhere`);
