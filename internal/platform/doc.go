// Package platform is the root of the reusable infrastructure packages.
//
// Packages below it import only the standard library, third-party libraries,
// and other platform packages. They take plain values (URLs, timeouts) and
// never the config package. scripts/arch-check.sh enforces this.
package platform
