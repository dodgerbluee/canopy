import { TestBed } from '@angular/core/testing';
import { describe, it, expect, afterEach } from 'vitest';
import { GardenOverview } from './garden-overview';
import { gardenMonths, type GardenPlant, type SeasonalFact } from './seasons-data';

const fact = (id: string, kind: SeasonalFact['kind'], months: number[]) =>
  ({
    id,
    kind,
    months,
    label: id,
    summary: 'Supported source guidance.',
    publisher: 'Extension',
    timingBasis: 'MONTH',
  }) as SeasonalFact;
const plant = (plantId: string, facts: SeasonalFact[]) =>
  ({ plantId, name: plantId, facts }) as GardenPlant;
const visible = new Set(['FERTILIZE', 'BLOOM', 'HARVEST', 'DORMANCY']);
const plants = [
  plant('Fig', [
    fact('feed-a', 'FERTILIZE', [2, 5, 9]),
    fact('feed-b', 'FERTILIZE', [5]),
    fact('bloom', 'BLOOM', [5]),
    fact('winter', 'DORMANCY', [12, 1, 2]),
  ]),
  plant('Lemon', [
    fact('feed-a', 'FERTILIZE', [5]),
    fact('ripe', 'HARVEST', [5]),
    fact('conditional', 'BLOOM', []),
  ]),
  plant('Unknown', []),
];
describe('whole garden aggregation', () => {
  it('counts each plant once per care month and group, retaining all source details', () => {
    const may = gardenMonths(plants, visible)[4];
    expect(may.carePlants).toBe(2);
    const feeding = may.groups.find((g) => g.kind === 'FERTILIZE')!;
    expect(feeding.plants.map((p) => p.plantId)).toEqual(['Fig', 'Lemon']);
    expect(feeding.windows).toHaveLength(3);
    expect(may.groups.findIndex((g) => g.kind === 'FERTILIZE')).toBeLessThan(
      may.groups.findIndex((g) => g.kind === 'BLOOM'),
    );
  });
  it('honors discrete months, winter boundaries and unknown timing without inventing work', () => {
    const months = gardenMonths(plants, visible);
    expect(months[2].groups).toEqual([]);
    expect(months[0].carePlants).toBe(0);
    expect(months[11].groups.map((g) => g.kind)).toEqual(['DORMANCY']);
    expect(
      months
        .flatMap((m) => m.groups.flatMap((g) => g.windows))
        .some((w) => w.fact.id === 'conditional'),
    ).toBe(false);
  });
  it('applies plant/type filters and suppresses duplicate facts without sharing plant identities', () => {
    const duplicate = plant('Fig', [...plants[0].facts, ...plants[0].facts]);
    const may = gardenMonths([duplicate], new Set(['FERTILIZE']))[4];
    expect(may.carePlants).toBe(1);
    expect(may.groups).toHaveLength(1);
    expect(may.groups[0].windows).toHaveLength(2);
    expect(gardenMonths(plants, new Set()).every((m) => m.groups.length === 0)).toBe(true);
  });
});
describe('whole garden overview interactions', () => {
  afterEach(() => TestBed.resetTestingModule());
  it('selects a month, opens grouped notes, then hands the source to the existing detail dialog', async () => {
    const f = TestBed.createComponent(GardenOverview);
    f.componentRef.setInput('plants', plants);
    f.componentRef.setInput('visibleKinds', visible);
    f.componentRef.setInput('year', 2026);
    f.componentRef.setInput('timezone', 'America/Chicago');
    f.detectChanges();
    (f.nativeElement.querySelectorAll('.garden-month')[4] as HTMLButtonElement).click();
    f.detectChanges();
    expect(f.nativeElement.querySelector('.month-heading').textContent).toContain('May');
    expect(f.nativeElement.querySelector('.month-heading').textContent).toContain('2 plants');
    (f.nativeElement.querySelector('.garden-group.kind-FERTILIZE') as HTMLButtonElement).click();
    f.detectChanges();
    await f.whenStable();
    expect(f.componentInstance.detail()?.windows).toHaveLength(3);
    let opened: unknown;
    f.componentInstance.inspect.subscribe((w) => (opened = w));
    const window = f.componentInstance.detail()!.windows[0];
    f.componentInstance.openSource(window);
    f.detectChanges();
    expect(opened).toBe(window);
    expect(f.componentInstance.detail()).toBeNull();
    f.componentRef.setInput('visibleKinds', new Set());
    f.detectChanges();
    expect(f.nativeElement.textContent).toContain('No sourced windows');
    expect(f.componentInstance.missingTiming()).toBe(3);
  });
});
