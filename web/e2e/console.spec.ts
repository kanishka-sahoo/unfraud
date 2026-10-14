import { expect, test } from "@playwright/test";

const profile = process.env.E2E_PROFILE ?? "tabformer";

test.beforeEach(({ page }) => {
  page.on("pageerror", (e) => {
    throw e;
  });
});

test("overview shows the dataset's figures and newest cases", async ({ page }) => {
  await page.goto(`/${profile}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByText("Events scored on arrival")).toBeVisible();
  await expect(page.getByRole("img", { name: /Decisions per minute/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Newest open cases" })).toBeVisible();
});

test("a case explains its score, and a disposition is recorded", async ({ page }) => {
  await page.goto(`/${profile}/cases?sort=score`);
  const first = page.locator("table.rows tbody tr").first().getByRole("link");
  const id = (await first.textContent())!.trim();
  await first.click();
  await expect(page).toHaveURL(new RegExp(`/cases/${id}$`));

  // The ledger opens at the base rate and closes at the case's probability.
  await expect(page.getByText("Base rate (the model's average case)")).toBeVisible();
  const close = page.locator(".ledger tr.balance td").last();
  await expect(close).toHaveText(/%$/);
  await expect(page.getByRole("heading", { name: "Analyst summary" })).toBeVisible();

  await page.getByLabel("Note for the record").fill("e2e: checked the payment graph");
  await page.getByRole("button", { name: "Escalate" }).click();
  await expect(page.getByText("Escalated: e2e: checked the payment graph")).toBeVisible();

  await page.goto(`/${profile}/cases?status=disposed&sort=recent`);
  await expect(page.getByRole("link", { name: id, exact: true })).toBeVisible();
});

test("the graph explorer draws a case's neighbourhood", async ({ page }) => {
  await page.goto(`/${profile}/cases?sort=score`);
  const id = (await page.locator("table.rows tbody tr").first().getByRole("link").textContent())!.trim();
  await page.goto(`/${profile}/cases/${id}`);
  await page.getByRole("link", { name: "Explore its graph" }).click();
  await expect(page.getByRole("heading", { name: new RegExp(`Graph around`) })).toBeVisible();
  // A drawn neighbourhood, or an explained empty state: the payer has no
  // links in the current window, or graph-svc is not running.
  await expect(
    page.locator(".graph-canvas canvas").first()
      .or(page.getByText(/has no links in the current window|graph-svc is not running/)),
  ).toBeVisible();
});

test("the live stream connects", async ({ page }) => {
  await page.goto(`/${profile}/live`);
  await expect(page.getByRole("status")).toContainText("Connected", { timeout: 15_000 });
});

test("benchmarks show the generated results", async ({ page }) => {
  await page.goto("/benchmarks");
  await expect(page.getByRole("heading", { name: /tabformer: LightGBM/ })).toBeVisible();
  await expect(page.getByText("+ graph features").first()).toBeVisible();
});

test("replays are listed and a bad request is explained", async ({ page, request }) => {
  await page.goto("/replay");
  await expect(page.getByRole("heading", { name: "Recent replays" })).toBeVisible();
  const res = await request.post("/api/replays", { data: { profile: "", speed: -1 } });
  expect(res.status()).toBe(400);
});
