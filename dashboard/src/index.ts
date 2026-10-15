// The dashboard as a library, for builds that wrap it (workly-cloud). Import
// '@workly/dashboard/index.css' once as well.
export { Dashboard, type DashboardProps } from './Dashboard';
export { apiKeyAuth, type Auth } from './auth';
export type { GettingStarted, NavItem } from './context';
export { PageHeader } from './components/PageHeader';
export { CodePanel } from './components/CodePanel';
export { ConfirmButton } from './components/ConfirmButton';
export { CopyButton } from './components/CopyButton';
export { DisabledReason } from './components/DisabledReason';
export { FormGroup, FormRow } from './components/FormRows';
export { ErrorText, Muted } from './components/QueryState';
export { RelativeTime } from './components/RelativeTime';
export { Section } from './components/Section';
export { ListPager } from './components/ListControls';
export { usePage, type Page } from './hooks/usePage';
export { errorMessage } from './lib/api';
export * from './primitives/badge';
export * from './primitives/button';
export * from './primitives/card';
export * from './primitives/dialog';
export * from './primitives/input';
export * from './primitives/popover';
export * from './primitives/select';
export * from './primitives/table';
export * from './primitives/tabs';
