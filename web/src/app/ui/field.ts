import { Component, computed, input } from '@angular/core';

let nextFieldId = 1;

/** Label/hint/error wiring per the state contract: every control exposes
 * label + describedby relationships, and errors set aria-invalid.
 * NB: the input is `inputId` (not `id`) so it never lands on the host element. */
@Component({
  selector: 'ui-field',
  template: `
    <label class="field-label" [for]="inputId()">{{ label() }}</label>
    @if (control() === 'textarea') {
      <textarea
        class="input"
        [id]="inputId()"
        [rows]="rows()"
        [attr.aria-describedby]="describedBy()"
        [attr.aria-invalid]="error() ? 'true' : null"
        [attr.required]="required() || null"
      ></textarea>
    } @else {
      <input
        class="input"
        [id]="inputId()"
        [type]="type()"
        [attr.placeholder]="hint() && !showHintBelow() ? hint() : null"
        [attr.aria-describedby]="describedBy()"
        [attr.aria-invalid]="error() ? 'true' : null"
        [attr.required]="required() || null"
      />
    }
    @if (hint() && showHintBelow()) {
      <p class="field-hint" [id]="inputId() + '-hint'">{{ hint() }}</p>
    }
    @if (error()) {
      <p class="field-error" [id]="inputId() + '-error'">{{ error() }}</p>
    }
  `,
})
export class UiField {
  readonly label = input.required<string>();
  readonly control = input<'input' | 'textarea'>('input');
  readonly type = input('text');
  readonly hint = input('');
  readonly error = input('');
  readonly required = input(false);
  readonly rows = input(3);
  /** when the hint is a placeholder we must not also point describedBy at it */
  readonly showHintBelow = input(true);
  readonly inputId = input(`field-${nextFieldId++}`);

  readonly describedBy = computed(() => {
    const parts: string[] = [];
    if (this.hint() && this.showHintBelow()) parts.push(`${this.inputId()}-hint`);
    if (this.error()) parts.push(`${this.inputId()}-error`);
    return parts.join(' ') || null;
  });
}
