import { Nav } from "@/components/Nav";
import { IncidentWorkspace } from "@/components/IncidentWorkspace";

export default function IncidentPage() {
  return (
    <main className="flex-1 flex flex-col">
      <header className="border-b border-rule-1 px-8 py-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-sm font-medium tracking-wide text-ramp-100">keyholders / incident</h1>
          <p className="tabular text-xs text-ramp-40 mt-1">
            a package fell — who&apos;s exposed, and since when
          </p>
        </div>
        <Nav current="/incident" />
      </header>
      <IncidentWorkspace />
    </main>
  );
}
