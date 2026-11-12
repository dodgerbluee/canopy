/** True when the Escape keydown travelled through a native <select> whose
 * base-select popup is currently open. In that case the browser closes the
 * picker itself; hosts (dialog, drawer) must not treat the key as their own
 * dismissal. SSR-safe: no DOM globals beyond what the event already carries. */
export function nativePickerIsOpen(event: Event): boolean {
  if (typeof HTMLSelectElement === 'undefined') return false;
  const nodes: EventTarget[] =
    typeof event.composedPath === 'function'
      ? event.composedPath()
      : [event.target].filter((t): t is EventTarget => t !== null);
  for (const node of nodes) {
    if (!(node instanceof HTMLSelectElement)) continue;
    try {
      if (node.matches(':open')) return true;
    } catch {
      // selector unsupported in this engine — assume no native picker
      return false;
    }
  }
  return false;
}
