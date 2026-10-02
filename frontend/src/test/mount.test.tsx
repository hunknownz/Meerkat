import { act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { mount } from '../mount';
import { RUN_A, snapshot } from './fixtures';

describe('mount', () => {
  it('renders into a ShadowRoot, updates, and fully cleans up on repeated mount/destroy', async () => {
    const el = document.createElement('div');
    document.body.append(el);
    const add = vi.spyOn(window, 'addEventListener');
    for (let i = 0; i < 3; i++) {
      const h = mount(el, { snapshot: snapshot(), legacyActive: [] }, { readonly: true });
      const root = el.shadowRoot!;
      expect(root.querySelector('style')!.textContent).toContain('#meerkat-ui');
      expect(root.textContent).toContain('Agent-07');
      await act(async () => { h.update(snapshot({ runs: [] }), []); });
      expect(root.textContent).toContain('当前没有工作流运行');
      await act(async () => { h.setDisconnected('host closed'); });
      expect(root.textContent).toContain('host closed');
      await act(async () => { h.update({ bad: true }); });
      expect(root.textContent).toContain('快照不符合契约');
      h.destroy();
      h.destroy();
      expect(root.childNodes.length).toBe(0);
    }
    expect(document.head.querySelectorAll('style').length).toBe(0);
    add.mockRestore();
  });

  it('maps stop to onAction and disables it in readonly mode', async () => {
    const el = document.createElement('div');
    const onAction = vi.fn(async () => ({}));
    const h = mount(el, { snapshot: snapshot() }, { onAction });
    const root = el.shadowRoot!;
    await act(async () => { (root.querySelector('.row-btn') as HTMLButtonElement).click(); });
    const stop = [...root.querySelectorAll('button')].find((b) => b.textContent === '停止运行')!;
    await act(async () => { stop.click(); });
    expect(onAction).toHaveBeenCalledWith({ type: 'stop', runId: RUN_A, requestId: expect.any(String) });
    expect(root.textContent).toContain('停止请求已接受');
    h.destroy();
    const ro = mount(el, { snapshot: snapshot() }, { onAction, readonly: true });
    await act(async () => { (el.shadowRoot!.querySelector('.row-btn') as HTMLButtonElement).click(); });
    const btn = [...el.shadowRoot!.querySelectorAll('button')].find((b) => b.textContent === '停止运行') as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    ro.destroy();
  });
});
