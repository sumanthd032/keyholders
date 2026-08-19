import type { Metadata } from "next";
import { Geist, Geist_Mono, VT323 } from "next/font/google";
import { CommandPalette } from "@/components/CommandPalette";
import { CommandRegistryProvider } from "@/lib/commandRegistry";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

// Display face for the headline count only. A CRT terminal face reads as measurement equipment
// rather than a game or a marketing display, the distinction that ruled out a blockier 8-bit face.
const display = VT323({
  weight: "400",
  variable: "--font-display",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Keyholders",
  description: "How many people can execute code on your machine, and who are they.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} ${display.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col bg-ground text-ramp-100">
        <CommandRegistryProvider>
          {children}
          <CommandPalette />
        </CommandRegistryProvider>
      </body>
    </html>
  );
}
