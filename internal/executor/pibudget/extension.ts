// Pi's TypeScript loader resolves its official package alias. A native .mjs
// dynamic import from a temporary directory cannot resolve a global CLI package.
import { VERSION } from '@earendil-works/pi-coding-agent';
import bridge from './bridge.mjs';

export default async function (pi: unknown) {
  await bridge(pi, VERSION);
}
