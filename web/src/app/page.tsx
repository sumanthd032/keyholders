import { Nav } from "@/components/Nav";
import { AuditWorkspace } from "@/components/AuditWorkspace";

export default function Home() {
  return (
    <main className="flex-1 flex flex-col">
      <header className="border-b border-rule-1 px-8 py-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-sm font-medium tracking-wide text-ramp-100">keyholders</h1>
          <p className="tabular text-xs text-ramp-40 mt-1">
            how many people can execute code on your machine, and who are they
          </p>
        </div>
        <Nav current="/" />
      </header>
      <AuditWorkspace />
    </main>
  );
}
