import { ChangeDetectionStrategy, Component, computed, input, output, signal } from '@angular/core';
import { UiIcon } from '../../ui/icon';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { gardenMonths, type GardenPlant, type GardenWindow } from './seasons-data';

@Component({
  selector: 'garden-year-overview',
  imports: [UiIcon, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './garden-overview.html',
  styleUrls: ['./garden-overview.css', './seasons-kinds.css'],
})
export class GardenOverview {
  readonly plants = input.required<GardenPlant[]>();
  readonly visibleKinds = input.required<ReadonlySet<string>>();
  readonly year = input.required<number>();
  readonly timezone = input('UTC');
  readonly inspect = output<GardenWindow>();
  readonly monthNames = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  readonly fullMonths = [
    'January',
    'February',
    'March',
    'April',
    'May',
    'June',
    'July',
    'August',
    'September',
    'October',
    'November',
    'December',
  ];
  private readonly now = new Date();
  readonly chosenMonth = signal<number | null>(null);
  readonly month = computed(
    () =>
      this.chosenMonth() ??
      Number(
        new Intl.DateTimeFormat('en-US', { timeZone: this.timezone(), month: 'numeric' }).format(
          this.now,
        ),
      ),
  );
  readonly months = computed(() => gardenMonths(this.plants(), this.visibleKinds()));
  readonly selected = computed(() => this.months()[this.month() - 1]);
  readonly selectedKind = signal<string | null>(null);
  readonly detail = computed(
    () => this.selected().groups.find((g) => g.kind === this.selectedKind()) ?? null,
  );
  readonly missingTiming = computed(
    () =>
      this.plants().filter(
        (p) => !p.facts.some((f) => f.months.length && this.visibleKinds().has(f.kind)),
      ).length,
  );
  selectMonth(month: number): void {
    this.selectedKind.set(null);
    this.chosenMonth.set(month);
  }
  openSource(window: GardenWindow): void {
    this.selectedKind.set(null);
    this.inspect.emit(window);
  }
}
