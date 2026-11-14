export default function HomePage() {
  return (
    <main
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        padding: "24px",
        backgroundColor: "var(--bg-canvas)",
        color: "var(--text-1)",
      }}
    >
      <section
        style={{
          maxWidth: "560px",
          width: "100%",
          backgroundColor: "var(--bg-panel)",
          border: "1px solid var(--line)",
          padding: "20px",
        }}
      >
        <h1 style={{ margin: "0 0 8px", fontSize: "16px", fontWeight: 600 }}>
          PyroSync
        </h1>
        <p style={{ margin: "0 0 12px", color: "var(--text-2)" }}>
          Phase 0 workspace initialized. Ready for Phase 1 ingestion and Phase 4
          command center implementation.
        </p>
        <p className="mono" style={{ margin: 0, fontSize: "12px", color: "var(--text-3)" }}>
          MODIS C6.1 + VIIRS 375 m | Equal-Area Cosine Grid | CARTO Dark Matter
        </p>
      </section>
    </main>
  );
}
