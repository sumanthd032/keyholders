"use client";

import { useEffect } from "react";

/**
 * Root error boundary: catches a render exception anywhere below the layout, in any of the three
 * views, rather than leaving the whole tool blank. Every view already handles its own fetch/stream
 * failures with local state; this is the backstop for the class those states cannot cover, a bug
 * that throws during render itself.
 */
export default function Error({
  error,
  retry,
}: {
  error: Error & { digest?: string };
  retry: () => void;
}) {
  useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <div className="flex-1 flex flex-col items-center justify-center gap-3 px-8" role="alert">
      <p className="text-sm text-danger">something went wrong rendering this view</p>
      <p className="text-xs text-ramp-40 max-w-md text-center">{error.message}</p>
      <button
        onClick={() => retry()}
        className="text-xs text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
      >
        try again
      </button>
    </div>
  );
}
