/** Shared a11y helpers for the ui layer. */

/** Focus the first control marked invalid (forms contract). */
export function focusFirstInvalid(root: ParentNode): HTMLElement | null {
  const el = root.querySelector<HTMLElement>('[aria-invalid="true"]');
  el?.focus();
  return el;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function firstFocusable(root: ParentNode): HTMLElement | null {
  return root.querySelector<HTMLElement>(FOCUSABLE);
}

export function focusables(root: ParentNode): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE));
}
