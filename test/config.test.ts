import { expect, test } from "bun:test";
import { createConfig } from "../src/config";

test("createConfig derives the listing URL from the selected league slug", () => {
  const config = createConfig({ leagueSlug: "foo-league" });

  expect(config.leagueSlug).toBe("foo-league");
  expect(config.listingUrl).toBe("https://braacket.com/league/foo-league/tournament");
});

test("createConfig requires an explicit league slug", () => {
  const previous = process.env.BRAACKET_LEAGUE_SLUG;
  delete process.env.BRAACKET_LEAGUE_SLUG;

  expect(() => createConfig()).toThrow(
    "Missing Braacket league slug. Provide --league <slug> or set BRAACKET_LEAGUE_SLUG."
  );

  if (previous) {
    process.env.BRAACKET_LEAGUE_SLUG = previous;
  }
});
