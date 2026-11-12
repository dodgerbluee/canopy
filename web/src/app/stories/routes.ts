import { inject } from '@angular/core';
import { isDevMode } from '@angular/core';
import { CanActivateFn, Routes } from '@angular/router';

/** Visual fixtures are dev/test-only: production builds refuse these routes. */
export const devOnlyGuard: CanActivateFn = () => isDevMode();

export const STORIES_ROUTES: Routes = [
  {
    path: 'stories',
    canActivate: [devOnlyGuard],
    loadComponent: () => import('./shell').then((m) => m.StoriesShell),
    children: [
      { path: '', loadComponent: () => import('./index').then((m) => m.StoryIndex) },
      { path: 'today', loadComponent: () => import('./today').then((m) => m.StoryToday) },
      { path: 'plants', loadComponent: () => import('./collection').then((m) => m.StoryCollection) },
      { path: 'plant', loadComponent: () => import('./plant').then((m) => m.StoryPlant) },
      { path: 'capture', loadComponent: () => import('./capture').then((m) => m.StoryCapture) },
      { path: 'compare', loadComponent: () => import('./compare').then((m) => m.StoryCompare) },
      { path: 'purchase', loadComponent: () => import('./purchase').then((m) => m.StoryPurchase) },
      { path: 'purchases', loadComponent: () => import('./purchases').then((m) => m.StoryPurchases) },
      { path: 'insight', loadComponent: () => import('./insight').then((m) => m.StoryInsight) },
      { path: 'credits', loadComponent: () => import('./index').then((m) => m.StoryCreditsPage) },
    ],
  },
];


