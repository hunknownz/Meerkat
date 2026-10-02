import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './app.css';
import { Connected } from './Connected';
import { BrowserTransport } from './transport';
import { preferredTheme } from './theme';

const el = document.getElementById('root');
if (el) {
  const transport = new BrowserTransport();
  createRoot(el).render(<StrictMode><Connected transport={transport} initialTheme={preferredTheme()} /></StrictMode>);
  window.addEventListener('pagehide', () => transport.close(), { once: true });
}
