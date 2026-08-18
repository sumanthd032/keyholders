"use client";

import { useCallback, useRef, useState } from "react";

export function UploadZone({ onFile }: { onFile: (file: File) => void }) {
  const [dragging, setDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragging(false);
      const file = e.dataTransfer.files[0];
      if (file) onFile(file);
    },
    [onFile],
  );

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={handleDrop}
      onClick={() => inputRef.current?.click()}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") inputRef.current?.click();
      }}
      className={`mx-8 my-16 flex-1 flex flex-col items-center justify-center gap-3 border rounded-sm cursor-pointer transition-colors ${
        dragging ? "border-signal bg-surface-1" : "border-rule-2 hover:border-ramp-40"
      }`}
      style={{ minHeight: "40vh" }}
    >
      <p className="text-sm text-ramp-80">
        drop a <span className="tabular text-ramp-100">package-lock.json</span>,{" "}
        <span className="tabular text-ramp-100">pnpm-lock.yaml</span>, or{" "}
        <span className="tabular text-ramp-100">yarn.lock</span>
      </p>
      <p className="text-xs text-ramp-40">or click to choose a file</p>
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) onFile(file);
        }}
      />
    </div>
  );
}
