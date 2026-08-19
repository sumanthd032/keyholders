/**
 * Instant fallback for the moment between navigating to a route and its client bundle mounting.
 * Every view owns its own richer loading state once mounted (the roll call reveal, the streaming
 * traversal, the observatory's own fetch state); this only covers the gap before any of that exists.
 */
export default function Loading() {
  return (
    <div className="flex-1 flex items-center justify-center px-8" aria-live="polite">
      <p className="text-xs text-ramp-40">loading</p>
    </div>
  );
}
