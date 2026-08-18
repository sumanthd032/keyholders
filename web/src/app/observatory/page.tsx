import { Nav } from "@/components/Nav";
import { ObservatoryDashboard } from "@/components/ObservatoryDashboard";

export default function ObservatoryPage() {
  return (
    <main className="flex-1 flex flex-col">
      <header className="border-b border-rule-1 px-8 py-6 flex items-baseline justify-between">
        <div>
          <h1 className="text-sm font-medium tracking-wide text-ramp-100">
            keyholders / observatory
          </h1>
          <p className="tabular text-xs text-ramp-40 mt-1">
            where the ecosystem is load-bearing, estimated across the whole ingested graph
          </p>
        </div>
        <Nav current="/observatory" />
      </header>
      <ObservatoryDashboard />
    </main>
  );
}
