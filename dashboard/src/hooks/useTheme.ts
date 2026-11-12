import { useEffect, useState } from 'react';

export type Theme = 'light' | 'dark';

const STORAGE_KEY = 'workly.theme';

function getInitialTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'light' || stored === 'dark') return stored;
  } catch {
    // Storage can be unavailable (private mode); fall back to the system theme.
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(getInitialTheme);

  useEffect(() => {
    // Every color changes at once; without this, elements with a color
    // transition fade over while the rest has already switched.
    const root = document.documentElement;
    root.classList.add('theme-switching');
    root.classList.toggle('dark', theme === 'dark');
    // Apply the new colors while transitions are off, then allow them again.
    void getComputedStyle(document.body).color;
    const done = setTimeout(() => root.classList.remove('theme-switching'), 0);
    try {
      localStorage.setItem(STORAGE_KEY, theme);
    } catch {
      // Not persisted; the toggle still works for this page load.
    }
    return () => clearTimeout(done);
  }, [theme]);

  const toggleTheme = () => setTheme((current) => (current === 'dark' ? 'light' : 'dark'));

  return { theme, toggleTheme };
}
