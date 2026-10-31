import { useCallback, useEffect, useState } from "react";
import { events, projects, type ProjectsView } from "../mygo";
import { mockView } from "./fixture";

/**
 * The Projects page's one data source: the bridge snapshot plus the
 * `projects:view` event. A snapshot built before the one on screen never
 * replaces it (replies and events can arrive out of order); a slow poll is
 * the safety net for a missed event. `?mock` (dev builds only) serves the
 * fixture so the layout can be reviewed in a plain browser.
 */
const MOCK = import.meta.env.DEV && new URLSearchParams(window.location.search).has("mock");

export function useProjectsView(): ProjectsView | null {
  const [view, setView] = useState<ProjectsView | null>(MOCK ? mockView(window.location.search) : null);
  const apply = useCallback(
    (next: ProjectsView) => setView((prev) => (prev && next.seq < prev.seq ? prev : next)),
    [],
  );
  useEffect(() => {
    if (MOCK) return;
    const pull = () => projects.snapshot().then(apply).catch(() => {});
    pull();
    const off = events.projectsView.on(apply);
    const timer = window.setInterval(pull, 4000);
    return () => {
      off();
      window.clearInterval(timer);
    };
  }, [apply]);
  useEffect(() => {
    if (view?.theme) document.documentElement.dataset.theme = view.theme;
  }, [view?.theme]);
  return view;
}

/** Fire a bridge command; failures surface natively (toasts), never here. */
export function run(call: () => Promise<unknown> | void) {
  if (MOCK) return;
  try {
    void Promise.resolve(call()).catch(() => {});
  } catch {
    // no runtime (plain browser): nothing to do
  }
}

export const isMock = MOCK;
