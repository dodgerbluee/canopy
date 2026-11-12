import { Component, ElementRef, signal, viewChild } from "@angular/core";
import { TestBed } from "@angular/core/testing";
import { UiEntryDialog, UiEntryRegistry } from "./entry-dialog";

@Component({
  imports: [UiEntryDialog],
  template: `
    <button #trigger>Add plant</button>
    <ui-entry-dialog
      #entry
      [open]="isOpen()"
      [title]="title()"
      description="Create a new plant record."
      [busy]="busy()"
      [dirty]="dirty()"
      (dismissed)="dismissed = dismissed + 1"
    >
      <div class="form-section">
        <label class="field-label" for="nick">Nickname</label>
        <input class="input" id="nick" />
        <label class="field-label" for="light">Light</label>
        <select class="input" id="light">
          <option value="low">Low</option>
          <option value="bright">Bright</option>
        </select>
      </div>
      <div class="form-actions">
        <button type="button" class="btn" (click)="entry.requestClose()">
          Cancel
        </button>
        <button type="button" class="btn btn-primary">Save</button>
      </div>
    </ui-entry-dialog>
  `,
})
class Host {
  isOpen = signal(false);
  title = signal("Add plant");
  busy = signal(false);
  dirty = signal<boolean | undefined>(undefined);
  dismissed = 0;
  readonly entry = viewChild.required<UiEntryDialog>("entry");
  readonly triggerEl =
    viewChild.required<ElementRef<HTMLButtonElement>>("trigger");
  trigger(): HTMLButtonElement {
    return this.triggerEl().nativeElement;
  }
}

@Component({
  imports: [UiEntryDialog],
  template: `
    <ui-entry-dialog #a [open]="openA()" title="Move plant">
      <input class="input" id="a-in" />
    </ui-entry-dialog>
    <ui-entry-dialog #b [open]="openB()" title="Add tag">
      <input class="input" id="b-in" />
    </ui-entry-dialog>
  `,
})
class TwoHost {
  openA = signal(false);
  openB = signal(false);
  readonly a = viewChild.required<UiEntryDialog>("a");
  readonly b = viewChild.required<UiEntryDialog>("b");
}

/** zoneless: effects/overlays tick on the app scheduler (microtask) */
async function settle(): Promise<void> {
  await new Promise((res) => setTimeout(res, 0));
}

function panels(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".dialog-panel"));
}

function panelTitle(panel: HTMLElement): string | undefined {
  const id = panel.getAttribute("aria-labelledby");
  return id
    ? (document.getElementById(id)?.textContent ?? undefined)
    : undefined;
}

/** CDK stacks nested overlays, so panel order is not the open order; locate a
 * panel by the accessible name its shell sets via aria-labelledby. */
function panelByTitle(title: string): HTMLElement {
  const panel = panels().find((p) => panelTitle(p) === title);
  if (!panel) {
    // diagnostic only: what the overlay container actually holds at the moment
    // the lookup fails, so a non-rendered panel is distinguishable from a
    // mislabelled one
    const diag = JSON.stringify(
      panels().map((p) => ({ title: panelTitle(p), html: p.outerHTML })),
    );
    throw new Error(`no dialog panel titled "${title}" panels=${diag}`);
  }
  return panel;
}

function escape(el: HTMLElement): void {
  el.dispatchEvent(
    new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
  );
}

function type(el: HTMLElement, value: string): void {
  (el as HTMLInputElement).value = value;
  el.dispatchEvent(new Event("input", { bubbles: true }));
}

function buttonByText(
  panel: HTMLElement,
  label: string,
): HTMLButtonElement | undefined {
  return Array.from(panel.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => b.textContent?.trim() === label,
  );
}

async function openHost(
  host: Host,
  fixture: {
    detectChanges(): void;
    whenStable(): Promise<void>;
    changeDetectorRef: { markForCheck(): void };
  },
): Promise<void> {
  host.isOpen.set(true);
  // Angular 22 defaults host components to OnPush: signal writes need
  // markForCheck() before detectChanges() or the input never propagates
  fixture.changeDetectorRef.markForCheck();
  fixture.detectChanges();
  // app scheduler settles on its own; a fixed setTimeout can race it
  await fixture.whenStable();
}

describe("UiEntryDialog", () => {
  afterEach(() => (document.body.innerHTML = ""));

  async function setup(): Promise<{
    fixture: ReturnType<typeof createHost>;
    host: Host;
  }> {
    const fixture = createHost();
    fixture.detectChanges();
    await fixture.whenStable();
    return { fixture, host: fixture.componentInstance };
  }
  function createHost() {
    return TestBed.createComponent(Host);
  }

  it("opens declaratively, projects content into .entry-form, disposes without dismissed", async () => {
    const { fixture, host } = await setup();
    expect(panels().length).toBe(0);
    await openHost(host, fixture);
    // diagnostic: separates input propagation vs effect vs detached OverlayContainer
    expect(host.entry().open()).toBe(true);
    expect(host.entry().isOpen()).toBe(true);
    expect(TestBed.inject(UiEntryRegistry).size).toBe(1);
    expect(panels().length).toBe(1);
    const panel = panels()[0];
    expect(panelTitle(panel)).toBe("Add plant");
    expect(panel.getAttribute("aria-describedby")).not.toBeNull();
    // consumer content projected inside the shared wrapper — native select kept
    expect(panel.querySelector(".entry-form #nick")).not.toBeNull();
    expect(
      panel.querySelector(".entry-form select.input#light"),
    ).not.toBeNull();
    host.isOpen.set(false);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    expect(panels().length).toBe(0);
    expect(host.dismissed).toBe(0);
  });

  it("dirty dismissal asks Keep editing / Discard and preserves the draft + focus", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    const nick = panels()[0].querySelector("#nick") as HTMLInputElement;
    nick.focus();
    type(nick, "Monstera");
    escape(panels()[0]);
    await settle();
    expect(panels().length).toBe(2);
    const confirm = panels()[1];
    expect(panelTitle(confirm)).toBe("Discard changes?");
    buttonByText(confirm, "Keep editing")!.click();
    await settle();
    expect(panels().length).toBe(1);
    expect((panels()[0].querySelector("#nick") as HTMLInputElement).value).toBe(
      "Monstera",
    );
    expect(document.activeElement).toBe(nick); // focus back inside the draft
    expect(host.dismissed).toBe(0);
  });

  it("Discard changes closes both dialogs and emits dismissed exactly once", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "Monstera");
    escape(panels()[0]);
    await settle();
    buttonByText(panels()[1], "Discard changes")!.click();
    await settle();
    expect(panels().length).toBe(0);
    expect(host.dismissed).toBe(1);
  });

  it("Cancel routes through the same dirty guard instead of closing", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "Monstera");
    buttonByText(panels()[0], "Cancel")!.click();
    await settle();
    expect(panels().length).toBe(2); // confirmation, not a closed draft
    expect(host.dismissed).toBe(0);
  });

  it("busy blocks every user dismissal and makes canLeave false", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "Monstera");
    host.busy.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    escape(panels()[0]);
    panels()[0].querySelector<HTMLButtonElement>(".dialog-close")!.click();
    buttonByText(panels()[0], "Cancel")!.click();
    await settle();
    expect(panels().length).toBe(1); // no confirmation, draft intact
    await expect(host.entry().canLeave()).resolves.toBe(false);
    expect(host.dismissed).toBe(0);
    // owner programmatic close after a successful save still works
    host.isOpen.set(false);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    expect(panels().length).toBe(0);
  });

  it("explicit dirty overrides auto tracking; markPristine approves leave until the next edit", async () => {
    const { fixture, host } = await setup();
    host.dirty.set(true); // e.g. ReactiveForms status binding
    await openHost(host, fixture);
    const leaving = host.entry().canLeave();
    await settle();
    expect(panels().length).toBe(2); // external dirty → confirmation
    buttonByText(panels()[1], "Keep editing")!.click();
    expect(await leaving).toBe(false);
    await settle();
    // successful save: approval wins even while the binding still says dirty
    host.entry().markPristine();
    await expect(host.entry().canLeave()).resolves.toBe(true);
    expect(panels().length).toBe(1);
    // next user edit cancels the approval and re-dirties
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "More text");
    const again = host.entry().canLeave();
    await settle();
    expect(panels().length).toBe(2);
    buttonByText(panels()[1], "Keep editing")!.click();
    expect(await again).toBe(false);
  });

  it("markPristine while busy approves canLeave; busy still blocks dismissal", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "Monstera");
    host.busy.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    // save in flight, no approval yet: canLeave false, dismissal blocked
    await expect(host.entry().canLeave()).resolves.toBe(false);
    escape(panels()[0]);
    buttonByText(panels()[0], "Cancel")!.click();
    await settle();
    expect(panels().length).toBe(1);
    expect(host.dismissed).toBe(0);
    // completed save while busy: canLeave true, dismissal still blocked
    host.entry().markPristine();
    await expect(host.entry().canLeave()).resolves.toBe(true);
    escape(panels()[0]);
    buttonByText(panels()[0], "Cancel")!.click();
    await settle();
    expect(panels().length).toBe(1);
    expect(host.dismissed).toBe(0);
    // markPristine during the same busy period keeps the approval
    host.entry().markPristine();
    await expect(host.entry().canLeave()).resolves.toBe(true);
    // a new edit clears the save approval even while busy
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "More text");
    await expect(host.entry().canLeave()).resolves.toBe(false);
    // markDirty clears it too
    host.entry().markPristine();
    await expect(host.entry().canLeave()).resolves.toBe(true);
    host.entry().markDirty();
    await expect(host.entry().canLeave()).resolves.toBe(false);
    // a new false->true busy transition clears it (markPristine during the
    // same busy period would have kept it)
    host.busy.set(false);
    host.entry().markPristine();
    await expect(host.entry().canLeave()).resolves.toBe(true);
    // flush the false tick first, so the next true is a fresh false->true edge
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    host.busy.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    await expect(host.entry().canLeave()).resolves.toBe(false);
    // owner programmatic close after the save still works
    host.isOpen.set(false);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    expect(panels().length).toBe(0);
    expect(host.dismissed).toBe(0);
  });

  it("title/busy/dirty updates never reopen the portal or lose form state", async () => {
    const { fixture, host } = await setup();
    await openHost(host, fixture);
    const panel = panels()[0];
    type(panel.querySelector("#nick") as HTMLInputElement, "Monstera");
    host.title.set("Saving plant");
    host.busy.set(true);
    host.dirty.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    expect(panels()[0]).toBe(panel); // same portal, not re-created
    expect((panel.querySelector("#nick") as HTMLInputElement).value).toBe(
      "Monstera",
    );
    expect(panelTitle(panel)).toBe("Saving plant"); // header updated reactively
  });

  it("programmatic close after save disposes and restores focus to the connected trigger", async () => {
    const { fixture, host } = await setup();
    host.trigger().focus();
    await openHost(host, fixture);
    type(panels()[0].querySelector("#nick") as HTMLInputElement, "Monstera");
    host.entry().markPristine();
    host.isOpen.set(false);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    expect(panels().length).toBe(0);
    expect(document.activeElement).toBe(host.trigger());
    expect(host.dismissed).toBe(0);
    expect(TestBed.inject(UiEntryRegistry).size).toBe(0);
  });
});

describe("UiEntryRegistry", () => {
  afterEach(() => (document.body.innerHTML = ""));

  it("checks open entries topmost-first and stops at the first refusal", async () => {
    const fixture = TestBed.createComponent(TwoHost);
    fixture.detectChanges();
    const host = fixture.componentInstance;
    host.openA.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await fixture.whenStable();
    host.openB.set(true);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await fixture.whenStable();
    const registry = TestBed.inject(UiEntryRegistry);
    expect(registry.size).toBe(2);
    host.a().markDirty();
    const decision = registry.canLeave();
    // the registry's async walk skips clean B and opens A's confirmation on
    // the app scheduler; a fixed settle can query before that panel renders
    await fixture.whenStable();
    expect(panels().length).toBe(3); // B clean, A refuses with a confirmation
    buttonByText(panelByTitle("Discard changes?"), "Keep editing")!.click();
    expect(await decision).toBe(false);
    await settle();
    expect(panels().length).toBe(2); // both drafts preserved
    // closing A (skipped afterwards) leaves a clean registry
    host.openA.set(false);
    fixture.changeDetectorRef.markForCheck();
    fixture.detectChanges();
    await settle();
    await expect(registry.canLeave()).resolves.toBe(true);
  });
});

describe("entry dialog field focus", () => {
 it("restores focus within its own projected form and safely ignores a closed dialog", async () => {
  TestBed.configureTestingModule({imports:[Host]});
  const fixture=TestBed.createComponent(Host),host=fixture.componentInstance;
  fixture.detectChanges();await openHost(host,fixture);
  const field=panels()[0].querySelector<HTMLInputElement>("#nick")!;
  host.trigger().focus();host.entry().focusFirstField();expect(document.activeElement).toBe(field);
  host.isOpen.set(false);fixture.detectChanges();await fixture.whenStable();host.entry().focusFirstField();
  fixture.destroy();
 });
});
