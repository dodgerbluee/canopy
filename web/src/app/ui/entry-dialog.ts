import {
  Component,
  DestroyRef,
  Injectable,
  TemplateRef,
  computed,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
  viewChild,
} from "@angular/core";
import { DialogRef, UiDialog } from "./dialog";
import { focusables } from "./a11y";

/** What the registry needs from an entry dialog instance. UiEntryDialog
 * satisfies this structurally; hosts may also implement it for custom flows. */
export interface UiEntryHandle {
  isOpen(): boolean;
  canLeave(): Promise<boolean>;
}

/**
 * Open entry dialogs, most recently opened last. A (later, core) route guard
 * awaits canLeave() before navigating: entries are checked in topmost order,
 * the first refusal stops the walk, closed/destroyed entries are skipped.
 * This is what preserves drafts across browser-back/route changes.
 * No data-layer/router/auth concerns live here.
 */
@Injectable({ providedIn: "root" })
export class UiEntryRegistry {
  private readonly entries: UiEntryHandle[] = [];

  register(entry: UiEntryHandle): void {
    if (!this.entries.includes(entry)) this.entries.push(entry);
  }

  unregister(entry: UiEntryHandle): void {
    const i = this.entries.indexOf(entry);
    if (i !== -1) this.entries.splice(i, 1);
  }

  get size(): number {
    return this.entries.length;
  }

  async canLeave(): Promise<boolean> {
    for (const entry of [...this.entries].reverse()) {
      if (!entry.isOpen()) continue;
      if (!(await entry.canLeave())) return false;
    }
    return true;
  }
}

/**
 * Declarative modal entry form on the existing UiDialog/CDK overlay (not a
 * parallel native <dialog> system). Consumers project their existing
 * content/forms (native selects and all) into the shared .entry-form wrapper;
 * no nested <form> is introduced.
 *
 * Lifecycle: opens its portal on false->true and disposes on false/destroy
 * without emitting `dismissed`. Title/description updates flow into the
 * shared shell reactively; busy/dirty changes never reopen the portal, so
 * form state survives.
 *
 * Dirty model: with no explicit `dirty` input the body tracks bubbled
 * input/change events automatically; an explicit dirty boolean (e.g. from
 * ReactiveForms status) overrides that tracking. markPristine() approves
 * leaving immediately even before an external dirty binding ticks; the next
 * user edit or markDirty() cancels the approval.
 *
 * Save approval: markPristine() while `busy` records a completed-save
 * approval, so canLeave() resolves true even though the busy input has not
 * ticked false yet. User dismissals stay blocked while busy. A new edit,
 * markDirty() or a new false->true busy transition clears the approval;
 * markPristine() during an already-busy period keeps it.
 *
 * Dismissal: X/Escape/backdrop/Cancel all call requestClose(). When dirty,
 * the same compact confirmation (Keep editing / Discard changes) appears;
 * the draft is preserved and focus returns inside the dialog. `dismissed`
 * emits only after the guard resolves true — the owner then flips `open`
 * or navigates. busy blocks every user dismissal and makes canLeave false;
 * the owner's programmatic close (open=false) after a successful save still
 * works.
 */
@Component({
  selector: "ui-entry-dialog",
  template: `
    <ng-template #formTpl>
      <div class="entry-form">
        <ng-content />
      </div>
    </ng-template>
    <ng-template #confirmTpl>
      <p class="confirm-copy">
        You have unsaved changes. Discarding closes this form.
      </p>
      <div class="form-actions">
        <button type="button" class="btn" (click)="keepEditing()">
          Keep editing
        </button>
        <button
          type="button"
          class="btn btn-primary"
          (click)="discardChanges()"
        >
          Discard changes
        </button>
      </div>
    </ng-template>
  `,
  styles: `
    :host {
      display: none;
    }
  `,
})
export class UiEntryDialog {
  private readonly dialogs = inject(UiDialog);
  private readonly registry = inject(UiEntryRegistry);
  private readonly destroyRef = inject(DestroyRef);

  /** declarative visibility — false->true opens, false/destroy disposes */
  readonly open = input(false);
  readonly title = input.required<string>();
  readonly description = input("");
  readonly size = input<"compact" | "regular" | "wide">("regular");
  /** save in flight: blocks every user dismissal, canLeave is false until a
   * completed-save approval (markPristine while busy) lands */
  readonly busy = input(false);
  /** explicit dirty (e.g. ReactiveForms) overrides automatic tracking */
  readonly dirty = input<boolean | undefined>(undefined);

  /** fires only after a guarded requestClose resolves true */
  readonly dismissed = output<void>();

  private readonly autoDirty = signal(false);
  private readonly forcedDirty = signal(false);
  private readonly approvedPristine = signal(false);
  /** completed-save approval recorded while busy; distinct from discard approval */
  private readonly saveApproved = signal(false);

  private dialogRef: DialogRef<void> | null = null;
  private confirmRef: DialogRef<void> | null = null;
  private confirmPromise: Promise<boolean> | null = null;
  private confirmResolve: ((leave: boolean) => void) | null = null;
  private trackedPanel: HTMLElement | null = null;

  private readonly formTpl =
    viewChild.required<TemplateRef<unknown>>("formTpl");
  private readonly confirmTpl =
    viewChild.required<TemplateRef<unknown>>("confirmTpl");

  constructor() {
    // a fresh save in flight invalidates any earlier completed-save approval;
    // markPristine() during an already-busy period keeps it
    let wasBusy = this.busy();
    effect(() => {
      const busy = this.busy();
      if (busy && !wasBusy) this.saveApproved.set(false);
      wasBusy = busy;
    });
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open) this.openDialog();
        else this.disposeDialog();
      });
    });
    this.destroyRef.onDestroy(() => this.disposeDialog());
  }

  /** Restore field focus after an asynchronous form disables its controls.
   * Search stays inside this dialog's portal, including projected forms. */
  focusFirstField(): void {
    if (!this.dialogRef) return;
    focusables(this.dialogRef.panel).find(el => el.matches('input:not([type="hidden"]), textarea, select'))?.focus();
  }

  isOpen(): boolean {
    return this.dialogRef !== null;
  }

  /** User dismissal (X / Escape / backdrop / Cancel). Guarded when dirty. */
  requestClose(): void {
    if (!this.dialogRef || this.busy() || this.confirmRef) return;
    if (!this.dirtyState()) {
      this.finishClose();
      return;
    }
    this.confirmDiscard().then((leave) => {
      if (leave && this.dialogRef) this.finishClose();
    });
  }

  /** Route-guard probe: same async confirmation, but never emits dismissed
   * and never navigates. A confirmed discard permits leave until the next
   * edit. */
  canLeave(): Promise<boolean> {
    if (this.busy() && !this.saveApproved()) return Promise.resolve(false);
    if (!this.dialogRef || !this.dirtyState()) return Promise.resolve(true);
    return this.confirmDiscard();
  }

  /** after a successful save/reload: permits immediate navigation even
   * before an external dirty binding ticks; while busy this also records a
   * completed-save approval so canLeave() resolves true before the busy
   * input ticks false */
  markPristine(): void {
    this.autoDirty.set(false);
    this.forcedDirty.set(false);
    this.approvedPristine.set(true);
    if (this.busy()) this.saveApproved.set(true);
  }

  markDirty(): void {
    this.approvedPristine.set(false);
    this.saveApproved.set(false);
    this.forcedDirty.set(true);
  }

  private readonly dirtyState = computed(() => {
    if (this.approvedPristine()) return false;
    if (this.forcedDirty()) return true;
    return this.dirty() ?? this.autoDirty();
  });

  /** any real user edit cancels a pristine approval and re-dirties */
  private readonly onEdit = () => {
    this.approvedPristine.set(false);
    this.saveApproved.set(false);
    this.autoDirty.set(true);
  };

  private openDialog(): void {
    if (this.dialogRef) return;
    const ref = this.dialogs.open<void>(this.formTpl(), {
      title: this.title,
      description: this.description,
      size: this.size(),
      disposeOnNavigation: false, // drafts survive route teardown
      closeRequest: () => this.requestClose(),
    });
    this.dialogRef = ref;
    ref.closed.subscribe(() => {
      if (this.dialogRef === ref) this.dialogRef = null;
      this.detachTrackers();
    });
    ref.panel.addEventListener("input", this.onEdit);
    ref.panel.addEventListener("change", this.onEdit);
    this.trackedPanel = ref.panel;
    this.registry.register(this);
  }

  private disposeDialog(): void {
    this.closeConfirmation(false);
    this.dialogRef?.close();
    this.dialogRef = null;
    this.detachTrackers();
    this.registry.unregister(this);
  }

  private finishClose(): void {
    this.disposeDialog();
    this.dismissed.emit();
  }

  /** one shared compact confirmation; duplicate requests reuse it */
  private confirmDiscard(): Promise<boolean> {
    if (this.confirmPromise) return this.confirmPromise;
    this.confirmPromise = new Promise<boolean>((resolve) => {
      this.confirmResolve = resolve;
      const ref = this.dialogs.open<void>(this.confirmTpl(), {
        title: "Discard changes?",
        size: "compact",
        disposeOnNavigation: false,
        closeRequest: () => this.keepEditing(),
      });
      this.confirmRef = ref;
      ref.closed.subscribe(() => {
        if (this.confirmRef === ref) this.confirmRef = null;
        if (this.confirmResolve) {
          const settle = this.confirmResolve;
          this.confirmResolve = null;
          settle(false);
        }
        this.confirmPromise = null;
      });
    });
    return this.confirmPromise;
  }

  keepEditing(): void {
    this.closeConfirmation(false);
  }

  discardChanges(): void {
    this.closeConfirmation(true);
  }

  private closeConfirmation(leave: boolean): void {
    const settle = this.confirmResolve;
    this.confirmResolve = null;
    const ref = this.confirmRef;
    this.confirmRef = null;
    ref?.close();
    if (leave) {
      // confirmed decision permits leave until another edit
      this.autoDirty.set(false);
      this.forcedDirty.set(false);
      this.approvedPristine.set(true);
    }
    settle?.(leave);
  }

  private detachTrackers(): void {
    if (!this.trackedPanel) return;
    this.trackedPanel.removeEventListener("input", this.onEdit);
    this.trackedPanel.removeEventListener("change", this.onEdit);
    this.trackedPanel = null;
  }
}
