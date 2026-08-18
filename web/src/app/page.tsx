export default function Home() {
  return (
    <main className="flex-1 flex flex-col">
      <header className="border-b border-rule-1 px-8 py-6">
        <h1 className="text-sm font-medium tracking-wide text-ramp-100">keyholders</h1>
        <p className="tabular text-xs text-ramp-40 mt-1">
          how many people can execute code on your machine, and who are they
        </p>
      </header>

      <section className="flex-1 flex flex-col items-center justify-center gap-2 px-8">
        <div className="display-numeral text-signal text-8xl leading-none">1,847</div>
        <p className="text-sm text-ramp-60">people can execute code on your machine</p>
      </section>

      <footer className="border-t border-rule-1 px-8 py-4 flex items-center gap-6">
        <span className="tabular text-xs text-ramp-60">
          coexistence <span className="text-ramp-100">1,847</span>
        </span>
        <span className="text-rule-2">|</span>
        <span className="tabular text-xs text-ramp-60">
          union graph <span className="text-ramp-100">2,203</span>
        </span>
        <span className="text-rule-2">|</span>
        <span className="tabular text-xs text-danger">phantom 356 (16.2%)</span>
      </footer>
    </main>
  );
}
