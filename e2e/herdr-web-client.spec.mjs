import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { expect, test } from 'playwright/test';
import { startHerdrFixture } from './fixture.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(here, '..');
const distRoot =
  process.env.HERDR_WEB_CLIENT_DIST || path.join(repositoryRoot, 'web', 'dist');
const e2e = test.extend({
  herdr: async ({ browserName }, use) => {
    void browserName;
    const fixture = await startHerdrFixture();
    try {
      await use(fixture);
    } finally {
      await fixture.stop();
    }
  },
});

e2e.describe.configure({ mode: 'serial' });

async function distFiles(directory, prefix = '') {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const fullPath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await distFiles(fullPath, relative)));
    } else if (entry.isFile()) {
      files.push({ relative, fullPath });
    }
  }
  return files.sort((left, right) =>
    left.relative.localeCompare(right.relative),
  );
}

async function waitForState(fixture, predicate, message) {
  await expect
    .poll(async () => predicate(await fixture.state()), {
      timeout: 20_000,
      intervals: [25, 50, 100, 250, 500],
      message,
    })
    .toBe(true);
}

function clientEvents(state, kind) {
  return state.client_events.filter((event) => !kind || event.kind === kind);
}

function decodedInputs(state) {
  return clientEvents(state, 'input').map((event) =>
    Buffer.from(event.data, 'base64').toString('utf8'),
  );
}

function latestClientStart(state) {
  return clientEvents(state, 'start').at(-1);
}

async function openReady(page, fixture) {
  await page.goto(`${fixture.origin}/`, { waitUntil: 'domcontentloaded' });
  await expect(page).toHaveTitle('Herdr');
  await expect(page.locator('#app')).toHaveAttribute(
    'data-connection',
    'ready',
    { timeout: 20_000 },
  );
  await waitForState(
    fixture,
    (state) =>
      state.session_requests === 1 &&
      state.nonce_count === 1 &&
      state.nonces.length === 1 &&
      state.websocket_connections === 1 &&
      state.hello_messages === 1 &&
      state.ready_messages === 1,
    'the authenticated session must establish one WebSocket hello/ready attachment',
  );
  await waitForState(
    fixture,
    (state) => clientEvents(state, 'start').length === 1,
    'the target must launch exactly one direct fake Herdr client',
  );
}

async function terminalPoint(page, verticalFraction) {
  const screen = page.locator('#terminal .xterm-screen');
  const box = await screen.boundingBox();
  expect(box, 'xterm screen must have a mobile hit area').not.toBeNull();
  return {
    x: box.x + box.width / 2,
    y: box.y + box.height * verticalFraction,
  };
}

async function tapTerminal(page) {
  const point = await terminalPoint(page, 0.5);
  await page.touchscreen.tap(point.x, point.y);
}

async function swipeTerminal(page, startFraction, endFraction) {
  const start = await terminalPoint(page, startFraction);
  const end = await terminalPoint(page, endFraction);
  const session = await page.context().newCDPSession(page);
  try {
    await session.send('Input.dispatchTouchEvent', {
      type: 'touchStart',
      touchPoints: [{ ...start, id: 1, force: 1 }],
    });
    const steps = 8;
    for (let step = 1; step <= steps; step += 1) {
      const progress = step / steps;
      await session.send('Input.dispatchTouchEvent', {
        type: 'touchMove',
        touchPoints: [
          {
            x: start.x + (end.x - start.x) * progress,
            y: start.y + (end.y - start.y) * progress,
            id: 1,
            force: 1,
          },
        ],
      });
    }
    await session.send('Input.dispatchTouchEvent', {
      type: 'touchEnd',
      touchPoints: [],
    });
  } finally {
    await session.detach();
  }
}

async function assertEmbeddedAssets(page) {
  const files = await distFiles(distRoot);
  expect(files.length).toBeGreaterThan(0);
  const remote = await page.evaluate(
    async (relativePaths) => {
      const encode = (bytes) => {
        let result = '';
        for (let offset = 0; offset < bytes.length; offset += 0x8000) {
          result += String.fromCharCode(
            ...bytes.subarray(offset, offset + 0x8000),
          );
        }
        return btoa(result);
      };
      const results = [];
      for (const relative of relativePaths) {
        const href = `/${relative.split('/').map(encodeURIComponent).join('/')}`;
        const response = await fetch(href, {
          cache: 'no-store',
          credentials: 'same-origin',
        });
        results.push({
          relative,
          status: response.status,
          body: encode(new Uint8Array(await response.arrayBuffer())),
        });
      }
      return results;
    },
    files.map(({ relative }) => relative),
  );

  expect(remote).toHaveLength(files.length);
  for (const result of remote) {
    const source = files.find(({ relative }) => relative === result.relative);
    expect(result.status, `${result.relative} status`).toBe(200);
    expect(result.body, `${result.relative} embedded body`).toBe(
      (await readFile(source.fullPath)).toString('base64'),
    );
  }

  const fontAssets = files.filter(({ relative }) =>
    /^assets\/CaskaydiaMonoNerdFontMono-Regular-[A-Z0-9]+\.ttf$/.test(relative),
  );
  expect(fontAssets).toHaveLength(1);
  const fontBytes = await readFile(fontAssets[0].fullPath);
  expect(createHash('sha256').update(fontBytes).digest('hex')).toBe(
    '0bc1e80eb7d1c0a1debb433a21da6e686b15556e1d54fcfe47f87f7379276830',
  );
  const fontState = await page.evaluate(async () => {
    const glyphs = '0\ue0b0\uf14a\udb84\udeb7';
    const loadedFaces = await document.fonts.load(
      '14px "Herdr Terminal Mono"',
      glyphs,
    );
    const canvas = document.createElement('canvas');
    const context = canvas.getContext('2d');
    context.font = '14px "Herdr Terminal Mono"';
    return {
      configuredFamily: getComputedStyle(
        document.documentElement,
      ).getPropertyValue('--font-terminal'),
      faceLoaded: loadedFaces.some(
        (face) =>
          face.family.replace(/^["']|["']$/g, '') === 'Herdr Terminal Mono' &&
          face.status === 'loaded',
      ),
      glyphsLoaded: document.fonts.check('14px "Herdr Terminal Mono"', glyphs),
      widths: [...glyphs].map((glyph) => context.measureText(glyph).width),
    };
  });
  expect(fontState.configuredFamily.trim()).toMatch(/^"Herdr Terminal Mono",/);
  expect(fontState.faceLoaded).toBe(true);
  expect(fontState.glyphsLoaded).toBe(true);
  expect(
    fontState.widths.every(
      (width) => Math.abs(width - fontState.widths[0]) < 0.01,
    ),
  ).toBe(true);
}

async function securityHeaders(page, origin) {
  const response = await page.evaluate(async () => {
    const result = await fetch(location.href, { cache: 'no-store' });
    return {
      status: result.status,
      headers: Object.fromEntries(result.headers.entries()),
    };
  });
  expect(response.status).toBe(200);
  expect(response.headers['cache-control']).toBe('no-store');
  expect(response.headers['content-security-policy']).toContain(
    "default-src 'none'",
  );
  expect(response.headers['content-security-policy']).toContain(
    `connect-src 'self' ${origin.replace(/^https:/, 'wss:')}`,
  );
  expect(response.headers['content-security-policy']).toContain(
    "frame-ancestors 'none'",
  );
  expect(response.headers['cross-origin-opener-policy']).toBe('same-origin');
  expect(response.headers['cross-origin-resource-policy']).toBe('same-origin');
  expect(response.headers['permissions-policy']).toContain('camera=()');
  expect(response.headers['referrer-policy']).toBe('no-referrer');
  expect(response.headers['strict-transport-security']).toBe(
    'max-age=31536000',
  );
  expect(response.headers['x-content-type-options']).toBe('nosniff');
  expect(response.headers['x-frame-options']).toBe('DENY');
}

async function assertTransportBoundary(fixture) {
  const state = await fixture.state();
  expect(state.origin).toMatch(/^https:\/\/127\.0\.0\.1:\d+$/);
  expect(state.issuer).toMatch(/^https:\/\/127\.0\.0\.1:\d+\/tenant-fixture$/);
  expect(state.jwks_url).toMatch(
    /^https:\/\/127\.0\.0\.1:\d+\/tenant-fixture\/jwks$/,
  );
  expect(state.target_path).toBe(fixture.artifact);
  expect(state.socket_path).toMatch(
    /herdr-web-client-testfixture-[^/]+\/herdr\.sock$/,
  );
  expect(state.oidc_issuer_requests).toBe(0);
  expect(state.oidc_jwks_requests).toBeGreaterThan(0);
  expect(state.forwarded_header_values.length).toBeGreaterThan(0);
  expect(state.session_markers).toEqual(['session']);
  expect(state.injected_headers).toBe(state.total_requests);
  expect(state.forwarded_header_values.every((count) => count === 1)).toBe(
    true,
  );
  expect(state.websocket_header_values).toEqual([1]);
  expect(state.websocket_hosts).toEqual([new URL(fixture.origin).host]);
  expect(state.websocket_origins).toEqual([fixture.origin]);
  expect(state.websocket_protocols).toEqual(['herdr-web-client.v1']);
}

async function assertDirectClientInvocation(fixture, state) {
  const start = latestClientStart(state);
  expect(start.argv).toEqual([
    expect.stringContaining('herdr-web-client-testfixture'),
    'client',
  ]);
  expect(start.cwd).toBe(path.dirname(state.socket_path));
  expect(start.env.HOME).toBe(path.dirname(state.socket_path));
  expect(start.env.TERM).toBe('xterm-256color');
  expect(start.env.LANG).toBe('C.UTF-8');
  const allowed = new Set([
    'HOME',
    'USER',
    'LOGNAME',
    'PATH',
    'TERM',
    'COLORTERM',
    'LANG',
    'LC_ALL',
    'LC_CTYPE',
    'LC_MESSAGES',
    'LC_MONETARY',
    'LC_NUMERIC',
    'LC_TIME',
    'XDG_RUNTIME_DIR',
  ]);
  expect(Object.keys(start.env).every((name) => allowed.has(name))).toBe(true);
  expect(Object.keys(start.env).some((name) => name.startsWith('HERDR_'))).toBe(
    false,
  );
  expect(start.env.SSL_CERT_FILE).toBeUndefined();
  expect(start.env.HERDR_ENV).toBeUndefined();
  expect(start.cols).toBeGreaterThanOrEqual(20);
  expect(start.rows).toBeGreaterThanOrEqual(5);
  expect(fixture.manifest.target_path).toBe(fixture.artifact);
  if (process.env.HERDR_E2E_SYSTEMD === '1') {
    const unit = execFileSync(
      '/usr/bin/systemctl',
      ['--user', 'whoami', String(start.pid)],
      { encoding: 'utf8' },
    ).trim();
    expect(unit).toMatch(/^herdr-web-client-attachment-[0-9a-f]{32}\.service$/);
  }
}

e2e(
  '@desktop serves the exact embedded production bundle and bridges PTY/completion state',
  async ({ page, herdr }) => {
    await openReady(page, herdr);
    await securityHeaders(page, herdr.origin);
    await assertEmbeddedAssets(page);
    await assertTransportBoundary(herdr);

    let state = await herdr.state();
    await assertDirectClientInvocation(herdr, state);
    await expect(page.locator('#terminal .xterm-rows')).toContainText(
      'FIXTURE_PTY_READY',
    );

    await page.locator('#terminal textarea').focus();
    await page.keyboard.insertText('fixture-desktop-input');
    await waitForState(
      herdr,
      (current) =>
        decodedInputs(current).join('').includes('fixture-desktop-input'),
      'PTY input must reach the direct herdr client',
    );
    await expect(page.locator('#terminal .xterm-rows')).toContainText(
      'FIXTURE_PTY_INPUT',
    );

    state = await herdr.state();
    const initialSize = latestClientStart(state);
    await page.setViewportSize({ width: 1024, height: 700 });
    await waitForState(
      herdr,
      (current) =>
        clientEvents(current, 'resize').some(
          (event) =>
            event.cols !== initialSize.cols || event.rows !== initialSize.rows,
        ),
      'PTY resize must be forwarded after a desktop viewport change',
    );
    await expect(page.locator('.desktop-toolbar')).toBeVisible();
    await expect(page.locator('.mobile-dock')).toBeHidden();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('.desktop-toolbar')).toBeHidden();
    await expect(page.locator('.mobile-dock')).toBeVisible();
    await page.setViewportSize({ width: 1024, height: 700 });
    await expect(page.locator('.desktop-toolbar')).toBeVisible();
    await expect(page.locator('.mobile-dock')).toBeHidden();

    await waitForState(
      herdr,
      (current) =>
        current.subscription_requests === 1 && current.snapshot_requests >= 2,
      'Herdr snapshot/subscribe/reconcile must be active',
    );
    await herdr.complete();
    await expect(page.locator('#completion-toast')).toBeVisible({
      timeout: 20_000,
    });
    await expect(page.locator('#completion-toast')).toContainText(
      'Fixture completed',
    );
    await waitForState(
      herdr,
      (current) =>
        current.completion_events === 1 && current.reconcile_snapshots >= 1,
      'a real pane.updated completion event must reach the browser after reconciliation',
    );
  },
);

e2e(
  '@mobile terminal taps and Type use native input without control refocus',
  async ({ page, herdr }) => {
    await openReady(page, herdr);
    const textarea = page.locator('#terminal textarea');
    await expect(textarea).toHaveAttribute('inputmode', 'text');
    await expect(textarea).toHaveAttribute('enterkeyhint', 'send');
    await expect(textarea).toHaveAttribute('autocomplete', 'on');
    await expect(textarea).toHaveAttribute('autocapitalize', 'sentences');
    await expect(textarea).toHaveAttribute('autocorrect', 'on');

    await tapTerminal(page);
    await expect(page.locator('#type-button')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement === element),
      )
      .toBe(true);
    await expect
      .poll(() => textarea.evaluate((element) => element.readOnly))
      .toBe(false);
    const keyInput = 'fixture-mobile-key-input';
    const imeInput = 'fixture-mobile-ime-input';
    await page.keyboard.type(keyInput);
    await page.locator('#type-button').click();
    await expect(page.locator('#type-button')).toHaveAttribute(
      'aria-pressed',
      'false',
    );
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement !== element),
      )
      .toBe(true);
    await page.locator('#type-button').click();
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement === element),
      )
      .toBe(true);
    await page.keyboard.insertText(imeInput);
    await waitForState(
      herdr,
      (state) => {
        const input = decodedInputs(state).join('');
        return input.includes(keyInput) && input.includes(imeInput);
      },
      'native mobile key and IME input must reach the PTY',
    );
    const mobileInput = decodedInputs(await herdr.state()).join('');
    expect(mobileInput).toBe(keyInput + imeInput);

    await page.getByRole('button', { name: 'Toggle Herdr switcher' }).click();
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement !== element),
      )
      .toBe(true);
    await expect(page.locator('#type-button')).toHaveAttribute(
      'aria-pressed',
      'false',
    );
    const mobileStart = latestClientStart(await herdr.state());
    const switchColumn = mobileStart.cols - Math.min(10, mobileStart.cols) + 2;
    const expectedSwitchSequence = `\u001b[<0;${switchColumn};2M\u001b[<0;${switchColumn};2m`;
    await waitForState(
      herdr,
      (state) => decodedInputs(state).join('').includes(expectedSwitchSequence),
      'mobile Switch must send the terminal mouse press/release sequence',
    );

    await page
      .getByRole('button', { name: 'Herdr', exact: true })
      .last()
      .click();
    const sheet = page.locator('#herdr-sheet');
    await expect(sheet).toBeVisible();
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement !== element),
      )
      .toBe(true);
    await expect(sheet.locator('[data-herdr-key="w"]')).toBeFocused();
    await sheet.locator('[data-herdr-key="w"]').click();
    await expect(sheet).toBeHidden();
    await expect
      .poll(() =>
        textarea.evaluate((element) => document.activeElement !== element),
      )
      .toBe(true);
    await waitForState(
      herdr,
      (state) => decodedInputs(state).join('').includes('\u0000w'),
      'Herdr menu selection must send its NUL-prefixed shortcut without keyboard refocus',
    );
  },
);

e2e(
  '@mobile touch gestures scroll terminal history',
  async ({ page, herdr }) => {
    await openReady(page, herdr);
    await tapTerminal(page);
    const trigger = 'fixture-scroll';
    await page.keyboard.insertText(trigger);
    await waitForState(
      herdr,
      (state) => decodedInputs(state).join('').includes(trigger),
      'scroll fixture trigger must reach the terminal',
    );

    const rows = page.locator('#terminal .xterm-rows');
    await expect(rows).toContainText('FIXTURE_SCROLL_LINE:119');
    const bottomRows = await rows.textContent();
    await swipeTerminal(page, 0.25, 0.85);
    await expect.poll(() => rows.textContent()).not.toBe(bottomRows);
    await expect(rows).toContainText('FIXTURE_SCROLL_LINE:');
    await expect(rows).not.toContainText('FIXTURE_SCROLL_LINE:119');

    await swipeTerminal(page, 0.85, 0.25);
    await swipeTerminal(page, 0.85, 0.25);
    await expect(rows).toContainText('FIXTURE_SCROLL_LINE:119');
  },
);

e2e(
  '@desktop a crashed Herdr client reports its exit and can start a fresh attachment',
  async ({ page, herdr }) => {
    await openReady(page, herdr);
    const firstPID = latestClientStart(await herdr.state()).pid;

    await page.locator('#terminal textarea').focus();
    await page.keyboard.insertText('fixture-crash');
    await expect(page.locator('#app')).toHaveAttribute(
      'data-connection',
      'ended',
    );
    await expect(page.locator('#connection-detail')).toContainText('code 23');
    await waitForState(
      herdr,
      (state) =>
        clientEvents(state, 'exit').some(
          (event) => event.pid === firstPID && event.exit_code === 23,
        ),
      'the crashed child exit must be observed before recovery',
    );

    await page.getByRole('button', { name: 'Start again' }).click();
    await expect(page.locator('#app')).toHaveAttribute(
      'data-connection',
      'ready',
    );
    await waitForState(
      herdr,
      (state) => {
        const starts = clientEvents(state, 'start');
        return starts.length >= 2 && starts.at(-1).pid !== firstPID;
      },
      'recovery must launch a distinct Herdr client process',
    );
    expect((await herdr.state()).target_exited).toBe(false);
  },
);

e2e(
  '@desktop closing the attachment cancels and reaps only its target child',
  async ({ page, herdr }) => {
    await openReady(page, herdr);
    const before = await herdr.state();
    const start = latestClientStart(before);
    const childPID = start.pid;
    await page.close();
    await expect
      .poll(
        () => {
          try {
            process.kill(childPID, 0);
            return false;
          } catch {
            return true;
          }
        },
        { timeout: 10_000, intervals: [25, 50, 100, 250] },
      )
      .toBe(true);
    await waitForState(
      herdr,
      (state) => state.websocket_closed >= 1,
      'the cancelled WebSocket must close before teardown',
    );
    const after = await herdr.state();
    expect(after.target_exited).toBe(false);
  },
);
