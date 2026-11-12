import { ValidatorFn } from '@angular/forms';

export const MIN_PASSWORD_LENGTH = 10;

/** Count characters consistently with the server, including non-BMP characters. */
export function hasMinimumPassword(value: string): boolean {
  return Array.from(value).length >= MIN_PASSWORD_LENGTH;
}

export const minimumPassword: ValidatorFn = (control) => {
  const value = typeof control.value === 'string' ? control.value : '';
  return hasMinimumPassword(value)
    ? null
    : {
        minlength: { requiredLength: MIN_PASSWORD_LENGTH, actualLength: Array.from(value).length },
      };
};
