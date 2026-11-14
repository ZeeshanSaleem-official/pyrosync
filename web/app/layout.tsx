import type { Metadata } from "next";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "maplibre-gl/dist/maplibre-gl.css";
import "../styles/tokens.css";
import "../styles/dashboard.css";
import { Providers } from "@/components/Providers";

export const metadata: Metadata = {
  title: "PyroSync — Harmonized MODIS & VIIRS Active-Fire Explorer",
  description:
    "Sensor-agnostic active-fire harmonization across NASA FIRMS MODIS Collection 6.1 and VIIRS 375 m observations.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
