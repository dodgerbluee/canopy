import { inject } from "@angular/core";
import type { CanDeactivateFn } from "@angular/router";

import { SessionService } from "./auth/session";
import { UiEntryRegistry } from "../ui/entry-dialog";

/**
 * One entry guard for every route that can host an entry dialog.
 *
 * The registry knows what is actually open: an entry dialog that has not been
 * dismissed, or an inline modal that asked for it (the receipt unlink
 * confirmation). When nothing is open `canLeave()` is true, so this guard is a
 * no-op for normal navigation.
 *
 * Signed-out visitors have nothing to lose — the write would be refused at the
 * gateway anyway — so the guard lets them through without a prompt.
 */
export const entryGuard: CanDeactivateFn<unknown> = () => {
  const session = inject(SessionService);
  if (session.state() !== "authed") return true;
  return inject(UiEntryRegistry).canLeave();
};
