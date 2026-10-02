// Copies build outputs into internal/web/assets (tracked, embedded by internal/web/embed.go).
import { cp, rm, mkdir, copyFile, readFile, writeFile } from 'node:fs/promises';

const root = new URL('../', import.meta.url);
const out = new URL('../internal/web/assets/', root);
await rm(out, { recursive: true, force: true });
await mkdir(new URL('mount/', out), { recursive: true });
await mkdir(new URL('mcp/', out), { recursive: true });
await cp(new URL('dist/app/', root), new URL('app/', out), { recursive: true });
await copyFile(new URL('dist/mount/meerkat-ui.js', root), new URL('mount/meerkat-ui.js', out));
// Companion CSS for hosts that inject styles themselves; the mount also embeds it into its ShadowRoot.
await copyFile(new URL('src/app.css', root), new URL('mount/meerkat-ui.css', out));
// app.css adapts Magpie (MIT); ship the authoritative notice with every distributed bundle.
const notice = new URL('../dashboard/public/THIRD_PARTY_NOTICES.md', root);
if (!/MIT License/.test(await readFile(notice, 'utf8'))) throw new Error('THIRD_PARTY_NOTICES.md must carry the Magpie MIT license');
for (const dir of ['app/', 'mount/']) await copyFile(notice, new URL(`${dir}THIRD_PARTY_NOTICES.md`, out));
const js = await readFile(new URL('mount/meerkat-ui.js', out), 'utf8');
if (/\bimport\s*\(|^import\s|^export\s|https?:\/\/(?!www\.w3\.org|react\.dev|reactjs\.org)/m.test(js.replace(/"[^"\n]*"/g, '""'))) {
  throw new Error('mount bundle must be self-contained (no imports/exports/network URLs)');
}
// MCP Apps resource: one self-contained HTML file (inline JS with CSS embedded, no external refs).
const mcpJs = await readFile(new URL('dist/mcp/meerkat-app.js', root), 'utf8');
if (/\bimport\s*\(|^import\s|^export\s/m.test(mcpJs.replace(/"[^"\n]*"/g, '""'))) throw new Error('MCP app bundle must not contain imports/exports');
const noticeText = await readFile(notice, 'utf8');
if (/--|<|>/.test(noticeText)) throw new Error('THIRD_PARTY_NOTICES.md cannot be embedded as an HTML comment');
const inlineJs = mcpJs.replace(/<\/(script)/gi, '<\\/$1').replace(/<!--/g, '<\\!--');
const mcpHtml = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Meerkat</title>
<!--
${noticeText.trim()}
-->
<style>html,body{margin:0;padding:0;background:transparent}</style>
</head>
<body>
<div id="root"></div>
<script>
${inlineJs}
</script>
</body>
</html>
`;
if (/<(?:script|link|img|iframe)\b[^>]*\s(?:src|href)\s*=/i.test(mcpHtml)) throw new Error('MCP app HTML must not reference external files');
if ((mcpHtml.match(/<\/script/gi) ?? []).length !== 1) throw new Error('MCP app HTML must contain exactly one inline script');
await writeFile(new URL('mcp/meerkat-app.html', out), mcpHtml);
await writeFile(new URL('.gitattributes', out), '** linguist-generated=true\n.gitattributes -linguist-generated\n');
