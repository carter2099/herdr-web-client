import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = path.dirname(fileURLToPath(import.meta.url));
const web = path.join(root, 'web');
const dist = process.env.HERDR_WEB_CLIENT_BUILD_DIST
  ? path.resolve(process.env.HERDR_WEB_CLIENT_BUILD_DIST)
  : path.join(web, 'dist');

await rm(dist, { recursive: true, force: true });
await mkdir(dist, { recursive: true });

const browserLicenseBanner = `/*!
Herdr Web Client browser bundle

${await readFile(path.join(root, 'LICENSE'), 'utf8')}

@xterm/xterm

${await readFile(path.join(root, 'node_modules', '@xterm', 'xterm', 'LICENSE'), 'utf8')}

@xterm/addon-fit

${await readFile(path.join(root, 'node_modules', '@xterm', 'addon-fit', 'LICENSE'), 'utf8')}
*/`;

const result = await build({
  absWorkingDir: root,
  entryPoints: { app: path.join(web, 'app.js') },
  outdir: dist,
  bundle: true,
  splitting: false,
  format: 'esm',
  platform: 'browser',
  target: ['es2022'],
  minify: true,
  sourcemap: false,
  legalComments: 'eof',
  loader: { '.ttf': 'file' },
  // Content-hashed names let the server mark every bundle file immutable while
  // index.html stays no-store and always names the current release.
  entryNames: 'assets/[name]-[hash]',
  assetNames: 'assets/[name]-[hash]',
  metafile: true,
  banner: {
    js: browserLicenseBanner,
    css: browserLicenseBanner,
  },
  charset: 'utf8',
  treeShaking: true,
  logLevel: 'info',
});

const outputs = Object.entries(result.metafile.outputs).map(
  ([output, metadata]) => ({
    href: `/${path.relative(dist, path.resolve(root, output)).split(path.sep).join('/')}`,
    metadata,
  }),
);

function onlyOutput(description, predicate) {
  const matches = outputs.filter(predicate);
  if (matches.length !== 1) {
    throw new Error(
      `expected one ${description} output, found ${matches.length}`,
    );
  }
  return matches[0].href;
}

const scriptHref = onlyOutput(
  'entry script',
  ({ href, metadata }) => href.endsWith('.js') && metadata.entryPoint,
);
const stylesheetHref = onlyOutput('stylesheet', ({ href }) =>
  href.endsWith('.css'),
);
const fontHref = onlyOutput('terminal font', ({ href }) =>
  href.endsWith('.ttf'),
);

// Each template placeholder must occur exactly once so a template edit cannot
// silently ship a page that references a missing bundle file.
let indexHTML = await readFile(path.join(web, 'index.html'), 'utf8');
for (const [placeholder, href] of [
  ['/assets/terminal-font', fontHref],
  ['/app.css', stylesheetHref],
  ['/app.js', scriptHref],
]) {
  const attribute = `"${placeholder}"`;
  if (indexHTML.split(attribute).length !== 2) {
    throw new Error(`web/index.html must reference ${attribute} exactly once`);
  }
  indexHTML = indexHTML.replace(attribute, `"${href}"`);
}

await Promise.all([
  writeFile(path.join(dist, 'index.html'), indexHTML),
  copyFile(path.join(web, 'favicon.png'), path.join(dist, 'favicon.png')),
  copyFile(path.join(web, 'favicon.ico'), path.join(dist, 'favicon.ico')),
  copyFile(
    path.join(web, 'fonts', 'LICENSE-CASCADIA-MONO.txt'),
    path.join(dist, 'LICENSE-CASCADIA-MONO.txt'),
  ),
  copyFile(
    path.join(web, 'fonts', 'LICENSE-NERD-FONTS.txt'),
    path.join(dist, 'LICENSE-NERD-FONTS.txt'),
  ),
  copyFile(
    path.join(web, 'fonts', 'NOTICE-NERD-FONTS.txt'),
    path.join(dist, 'NOTICE-NERD-FONTS.txt'),
  ),
]);
