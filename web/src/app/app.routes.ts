import { Routes } from '@angular/router';
import { anonGuard, authGuard } from './core/auth/guard';
import { entryGuard } from './core/entry-guard';
import { STORIES_ROUTES } from './stories/routes';

export const routes: Routes = [
  {
    path: 'setup/claim',
    loadComponent: () => import('./features/auth/setup').then((m) => m.SetupPage),
  },
  {
    path: 'setup',
    canActivate: [anonGuard],
    loadComponent: () => import('./features/auth/setup').then((m) => m.SetupPage),
  },
  {
    path: 'login',
    canActivate: [anonGuard],
    loadComponent: () => import('./features/auth/login').then((m) => m.LoginPage),
  },
  {
    path: 'rounds/:plantId',
    canActivate: [authGuard],
    loadComponent: () => import('./features/rounds/rounds').then((m) => m.RoundPage),
  },
  {
    path: '',
    canActivate: [authGuard],
    loadComponent: () => import('./shell/shell').then((m) => m.AppShell),
    children: [
      {
        path: '',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/today/today').then((m) => m.TodayPage),
      },
      // Collection is the parent outlet for `plants/new`; the router backtracks
      // through it for `plants/:id`, which stays a sibling so detail replaces
      // the list rather than rendering behind it.
      {
        path: 'plants',
        canDeactivate: [entryGuard],
        loadComponent: () =>
          import('./features/collection/collection').then((m) => m.CollectionPage),
        children: [
          {
            path: 'new',
            canDeactivate: [entryGuard],
            loadComponent: () =>
              import('./features/collection/plant-form').then((m) => m.PlantForm),
          },
        ],
      },
      {
        path: 'plants/:id',
        canDeactivate: [entryGuard],
        loadComponent: () =>
          import('./features/collection/plant-detail').then((m) => m.PlantDetail),
        children: [
          {
            path: 'edit',
            canDeactivate: [entryGuard],
            loadComponent: () =>
              import('./features/collection/plant-form').then((m) => m.PlantForm),
          },
        ],
      },
      {
        path: 'plants/:id/compare',
        loadComponent: () =>
          import('./features/collection/compare-photos').then((m) => m.ComparePhotos),
      },
      {
        path: 'rounds',
        loadComponent: () => import('./features/rounds/rounds').then((m) => m.RoundPage),
      },
      {
        path: 'calendar/year',
        pathMatch: 'full',
        redirectTo: '/year',
      },
      {
        path: 'year',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/seasons/seasons').then((m) => m.SeasonsPage),
      },
      {
        path: 'calendar',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/calendar/calendar').then((m) => m.CalendarPage),
      },
      {
        path: 'journal',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/journal/journal').then((m) => m.JournalPage),
      },
      {
        path: 'care',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/care/care-agenda').then((m) => m.CareAgendaPage),
        children: [
          {
            path: 'plan/:plantId',
            canDeactivate: [entryGuard],
            loadComponent: () =>
              import('./features/care/plan-editor').then((m) => m.CarePlanEditor),
          },
        ],
      },
      {
        path: 'inbox',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/inbox/inbox').then((m) => m.InboxPage),
      },
      {
        path: 'purchases',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/purchases/purchases').then((m) => m.PurchasesPage),
        children: [
          {
            path: 'new',
            canDeactivate: [entryGuard],
            loadComponent: () =>
              import('./features/purchases/purchase-form').then((m) => m.PurchaseForm),
          },
          {
            path: ':id/edit',
            canDeactivate: [entryGuard],
            loadComponent: () =>
              import('./features/purchases/purchase-form').then((m) => m.PurchaseForm),
          },
        ],
      },
      {
        path: 'more',
        loadComponent: () => import('./features/navigation/more').then((m) => m.MorePage),
      },
      {
        path: 'insights',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/assistant/assistant').then((m) => m.AssistantPage),
      },
      {
        path: 'settings',
        canDeactivate: [entryGuard],
        loadComponent: () => import('./features/settings/settings').then((m) => m.SettingsPage),
      },
    ],
  },
  {
    path: 'release',
    loadComponent: () => import('./release-page').then((m) => m.ReleasePage),
  },
  ...STORIES_ROUTES,
];
