import { spawn } from 'node:child_process';
import { realpath } from 'node:fs/promises';

function requiredPath(name) {
  const value = process.env[name];
  if (!value || value.trim() === '') {
    throw new Error(`${name} must name an exact built artifact`);
  }
  return value;
}

function readManifest(processHandle) {
  return new Promise((resolve, reject) => {
    let buffer = '';
    let settled = false;
    const stderr = [];
    const finish = (callback, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(deadline);
      callback(value);
    };
    const deadline = setTimeout(() => {
      finish(
        reject,
        new Error(`test fixture did not become ready\n${stderr.join('')}`),
      );
    }, 20_000);
    processHandle.stderr.on('data', (chunk) => stderr.push(String(chunk)));
    processHandle.stdout.on('data', (chunk) => {
      buffer += String(chunk);
      let newline = buffer.indexOf('\n');
      while (newline >= 0) {
        const line = buffer.slice(0, newline).trim();
        buffer = buffer.slice(newline + 1);
        if (!line) continue;
        try {
          const manifest = JSON.parse(line);
          if (manifest.type === 'ready') {
            finish(resolve, manifest);
            return;
          }
        } catch {
          // The supervisor emits one JSON manifest; ignore diagnostic lines so
          // a target error cannot be mistaken for readiness.
        }
        newline = buffer.indexOf('\n');
      }
    });
    processHandle.once('error', (error) => finish(reject, error));
    processHandle.once('exit', (code, signal) => {
      finish(
        reject,
        new Error(
          `test fixture exited before ready (${code ?? signal})\n${stderr.join('')}`,
        ),
      );
    });
  });
}

async function getJSON(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: { Accept: 'application/json', ...(options.headers || {}) },
  });
  if (!response.ok) {
    throw new Error(`fixture endpoint ${url} returned ${response.status}`);
  }
  return response.json();
}

function processExit(processHandle) {
  if (processHandle.exitCode !== null || processHandle.signalCode !== null) {
    return Promise.resolve();
  }
  return new Promise((resolve) => processHandle.once('exit', resolve));
}

function processExitBy(processHandle, milliseconds) {
  if (processHandle.exitCode !== null || processHandle.signalCode !== null) {
    return Promise.resolve(true);
  }
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      processHandle.removeListener('exit', onExit);
      resolve(false);
    }, milliseconds);
    const onExit = () => {
      clearTimeout(timer);
      resolve(true);
    };
    processHandle.once('exit', onExit);
  });
}

export class HerdrFixture {
  static async start() {
    const artifact = await realpath(requiredPath('HERDR_WEB_CLIENT_ARTIFACT'));
    const executable = requiredPath('HERDR_WEB_CLIENT_FIXTURE');
    const processHandle = spawn(executable, ['--target', artifact], {
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, HERDR_WEB_CLIENT_ARTIFACT: artifact },
    });
    let manifest;
    try {
      manifest = await readManifest(processHandle);
      processHandle.stderr.pipe(process.stderr);
      if ((await realpath(manifest.target_path)) !== artifact) {
        throw new Error(
          `fixture launched ${manifest.target_path}, not ${artifact}`,
        );
      }
    } catch (error) {
      if (
        processHandle.exitCode === null &&
        processHandle.signalCode === null
      ) {
        processHandle.kill('SIGTERM');
      }
      await processExit(processHandle);
      throw error;
    }
    return new HerdrFixture(processHandle, manifest, artifact);
  }

  constructor(processHandle, manifest, artifact) {
    this.process = processHandle;
    this.manifest = manifest;
    this.artifact = artifact;
    this.origin = manifest.origin;
    this.controlURL = manifest.control_url;
  }

  async state() {
    return getJSON(`${this.controlURL}/state`);
  }

  async complete() {
    return getJSON(`${this.controlURL}/complete`, { method: 'POST' });
  }

  async shutdownEndpoint() {
    return getJSON(`${this.controlURL}/shutdown`, { method: 'POST' });
  }

  async stop() {
    if (this.process.exitCode === null && this.process.signalCode === null) {
      this.process.kill('SIGTERM');
      if (!(await processExitBy(this.process, 8_000))) {
        this.process.kill('SIGKILL');
        await processExit(this.process);
      }
    }
  }
}

export async function startHerdrFixture() {
  return HerdrFixture.start();
}
