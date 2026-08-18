import { useSyncExternalStore } from "react";

function subscribe(callback: () => void) {
  const query = window.matchMedia("(prefers-reduced-motion: reduce)");
  query.addEventListener("change", callback);
  return () => query.removeEventListener("change", callback);
}

/**
 * Shared across every animated view: the Roll Call reveal, the graph canvas's force-directed layout
 * settling, and any future one. Each honors the result by rendering its static equivalent, never by
 * skipping the underlying data.
 */
export function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    () => false, // server snapshot: no window, and every animation here is a client-only enhancement
  );
}
