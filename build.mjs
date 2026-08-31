import { copyFile, mkdir, readFile, rm } from 'node:fs/promises';
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

await build({
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
  banner: {
    js: browserLicenseBanner,
    css: browserLicenseBanner,
  },
  charset: 'utf8',
  treeShaking: true,
  logLevel: 'info',
});

await Promise.all([
  copyFile(path.join(web, 'index.html'), path.join(dist, 'index.html')),
  copyFile(path.join(web, 'favicon.png'), path.join(dist, 'favicon.png')),
  copyFile(path.join(web, 'favicon.ico'), path.join(dist, 'favicon.ico')),
]);
