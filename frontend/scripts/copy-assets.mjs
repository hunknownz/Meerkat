// Copies build outputs into internal/web/assets (tracked, embedded by internal/web/embed.go).
import { cp, rm, mkdir, copyFile, readFile, writeFile } from 'node:fs/promises';

const root = new URL('../', import.meta.url);
const out = new URL('../internal/web/assets/', root);
await rm(out, { recursive: true, force: true });
await mkdir(new URL('mount/', out), { recursive: true });
await cp(new URL('dist/app/', root), new URL('app/', out), { recursive: true });
await copyFile(new URL('dist/mount/meerkat-ui.js', root), new URL('mount/meerkat-ui.js', out));
// Companion CSS for hosts that inject styles themselves; the mount also embeds it into its ShadowRoot.
await copyFile(new URL('src/app.css', root), new URL('mount/meerkat-ui.css', out));
const js = await readFile(new URL('mount/meerkat-ui.js', out), 'utf8');
if (/\bimport\s*\(|^import\s|^export\s|https?:\/\/(?!www\.w3\.org|react\.dev|reactjs\.org)/m.test(js.replace(/"[^"\n]*"/g, '""'))) {
  throw new Error('mount bundle must be self-contained (no imports/exports/network URLs)');
}
await writeFile(new URL('.gitattributes', out), '** linguist-generated=true\n.gitattributes -linguist-generated\n');
