// Deliberately forbidden: ui primitives must not touch the data layer or
// generated contracts. Used by boundary_check fixtures.
import { PlantsDocument } from '@canopy/contracts';
export const bad = PlantsDocument;
