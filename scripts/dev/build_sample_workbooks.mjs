import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  Presentation,
  PresentationFile,
  SpreadsheetFile,
  Workbook,
} from "@oai/artifact-tool";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, "../..");
const outputDir = path.join(rootDir, "data", "sample-docs");

async function writeBlob(targetPath, blob) {
  await fs.writeFile(targetPath, new Uint8Array(await blob.arrayBuffer()));
}

async function buildWorkbook() {
  const workbook = Workbook.create();
  const summary = workbook.worksheets.add("Summary");
  const assumptions = workbook.worksheets.add("Assumptions");
  const timeline = workbook.worksheets.add("Timeline");

  summary.getRange("A1:F1").values = [[
    "Scenario",
    "Users",
    "Upload target",
    "Support load",
    "Model cost",
    "Owner",
  ]];
  summary.getRange("A2:F5").values = [
    ["Pilot", 40, 120, 6, 1800, "Ops"],
    ["Department rollout", 180, 540, 20, 6200, "IT"],
    ["Knowledge refresh", 260, 780, 24, 9100, "AI Team"],
    ["Executive review", 25, 60, 3, 900, "Program Lead"],
  ];
  summary.getRange("A8:B11").values = [
    ["Metric", "Value"],
    ["Total expected users", 505],
    ["Quarterly upload target", 1500],
    ["Estimated support tickets", 53],
  ];
  summary.freezePanes.freezeRows(1);
  summary.getRange("A1:F1").format.font = { bold: true, color: "#FFFFFF", size: 12 };
  summary.getRange("A1:F1").format.fill = "#1B3E5E";
  summary.getRange("A1:F11").format.columnWidthPx = 180;
  summary.getRange("A1:A11").format.columnWidthPx = 220;
  summary.getRange("B1:E11").format.columnWidthPx = 140;
  summary.getRange("F1:F11").format.columnWidthPx = 170;
  summary.getRange("A1:F11").format.rowHeightPx = 30;
  summary.getRange("A1:F11").format.wrapText = true;
  summary.getRange("B2:E5").format.horizontalAlignment = "center";
  summary.getRange("E2:E5").format.numberFormat = "\"¥\"#,##0";
  summary.getRange("B9:B11").format.numberFormat = "#,##0";

  assumptions.getRange("A1:B6").values = [
    ["Assumption", "Value"],
    ["Average docs per uploader", 3],
    ["Pilot answer latency target (s)", 8],
    ["Target citation accuracy", 0.95],
    ["Support staffing ratio", 0.12],
    ["Local model provider", "Ollama"],
  ];
  assumptions.freezePanes.freezeRows(1);
  assumptions.getRange("A1:B1").format.font = { bold: true, color: "#FFFFFF", size: 12 };
  assumptions.getRange("A1:B1").format.fill = "#355C7D";
  assumptions.getRange("A1:A6").format.columnWidthPx = 280;
  assumptions.getRange("B1:B6").format.columnWidthPx = 150;
  assumptions.getRange("A1:B6").format.rowHeightPx = 28;
  assumptions.getRange("A1:B6").format.wrapText = true;

  timeline.getRange("A1:D5").values = [
    ["Milestone", "Owner", "Week", "Output"],
    ["Contract freeze", "Backend", "W1", "API + payload schema"],
    ["Runtime sample parsers", "AI Runtime", "W2", "txt/md/json/pdf/docx/xlsx/pptx"],
    ["Admin upload UI", "Web", "W3", "drag-and-drop + status list"],
    ["Eval baseline", "QA", "W4", "seed set + refusal checks"],
  ];
  timeline.freezePanes.freezeRows(1);
  timeline.getRange("A1:D1").format.font = { bold: true, color: "#FFFFFF", size: 12 };
  timeline.getRange("A1:D1").format.fill = "#4B5C57";
  timeline.getRange("A1:A5").format.columnWidthPx = 220;
  timeline.getRange("B1:B5").format.columnWidthPx = 140;
  timeline.getRange("C1:C5").format.columnWidthPx = 90;
  timeline.getRange("D1:D5").format.columnWidthPx = 260;
  timeline.getRange("A1:D5").format.rowHeightPx = 28;
  timeline.getRange("A1:D5").format.wrapText = true;

  summary.charts.add("bar", {
    title: "Sample ingestion workload by scenario",
    categories: ["Pilot", "Department rollout", "Knowledge refresh", "Executive review"],
    series: [
      {
        name: "Upload target",
        values: [120, 540, 780, 60],
        fill: "accent1",
      },
    ],
    hasLegend: false,
    dataLabels: { showValue: true },
    barOptions: { direction: "column", grouping: "clustered", gapWidth: 90 },
    from: { row: 1, col: 7 },
    extent: { widthPx: 520, heightPx: 280 },
  });

  const png = await workbook.render({
    sheetName: "Summary",
    range: "A1:N18",
    format: "png",
    scale: 2,
  });
  await writeBlob(path.join(outputDir, "sample-budget-preview.png"), png);

  const xlsx = await SpreadsheetFile.exportXlsx(workbook);
  await xlsx.save(path.join(outputDir, "sample-budget.xlsx"));
}

async function buildPresentation() {
  const presentation = Presentation.create({
    slideSize: { width: 1280, height: 720 },
  });

  const slide1 = presentation.slides.add();
  slide1.background.fill = "#F4EFE4";
  const eyebrow = slide1.shapes.add({
    geometry: "textbox",
    position: { left: 72, top: 54, width: 320, height: 24 },
    fill: "none",
    line: { style: "solid", fill: "none", width: 0 },
  });
  eyebrow.text = "ATLAS KNOWLEDGE ROLLOUT";
  eyebrow.text.style = { fontSize: 12, bold: true, color: "#B95E38" };

  const title = slide1.shapes.add({
    geometry: "textbox",
    position: { left: 72, top: 92, width: 520, height: 150 },
    fill: "none",
    line: { style: "solid", fill: "none", width: 0 },
  });
  title.text = "Office document ingestion sample deck";
  title.text.style = { fontSize: 34, bold: true, color: "#1D2A25" };

  const body = slide1.shapes.add({
    geometry: "textbox",
    position: { left: 72, top: 244, width: 520, height: 200 },
    fill: "none",
    line: { style: "solid", fill: "none", width: 0 },
  });
  body.text =
    "This deck is a real PPTX sample for parsing titles, bullets, slide numbers, and chart objects during retrieval testing.";
  body.text.style = { fontSize: 19, color: "#4B5C57" };

  const panel = slide1.shapes.add({
    geometry: "roundRect",
    position: { left: 650, top: 110, width: 540, height: 360 },
    fill: { type: "solid", color: "#FFFDF8" },
    line: { style: "solid", fill: "#D9D1C4", width: 1 },
    borderRadius: "rounded-2xl",
  });
  panel.text =
    "Slide parsing checks\n• slide title extraction\n• bullet preservation\n• source locator = slide number + title\n• optional speaker notes evaluation";
  panel.text.style = { fontSize: 18, color: "#31423E" };

  const slide2 = presentation.slides.add();
  slide2.background.fill = "#FFF9F0";
  const s2Title = slide2.shapes.add({
    geometry: "textbox",
    position: { left: 72, top: 60, width: 700, height: 42 },
    fill: "none",
    line: { style: "solid", fill: "none", width: 0 },
  });
  s2Title.text = "Rollout milestones";
  s2Title.text.style = { fontSize: 30, bold: true, color: "#1D2A25" };

  slide2.tables.add({
    rows: 5,
    columns: 3,
    left: 72,
    top: 128,
    width: 660,
    height: 300,
    values: [
      ["Milestone", "Owner", "Outcome"],
      ["Contract freeze", "Backend", "Upload/config APIs"],
      ["Parser baseline", "AI Runtime", "7-format parser set"],
      ["Admin workflow", "Web", "Upload + status UI"],
      ["Eval baseline", "QA", "Seed eval + refusals"],
    ],
  });

  slide2.charts.add("bar", {
    position: { left: 760, top: 150, width: 410, height: 250 },
    categories: ["APIs", "Parsers", "UI", "Evals"],
    series: [{ name: "Completion target", values: [100, 100, 100, 100], fill: "accent1" }],
    hasLegend: false,
    dataLabels: { showValue: true },
    barOptions: { direction: "column", grouping: "clustered", gapWidth: 100 },
  });

  const slide3 = presentation.slides.add();
  slide3.background.fill = "#EEF4F7";
  const s3Title = slide3.shapes.add({
    geometry: "textbox",
    position: { left: 72, top: 60, width: 680, height: 40 },
    fill: "none",
    line: { style: "solid", fill: "none", width: 0 },
  });
  s3Title.text = "Expected citation locators";
  s3Title.text.style = { fontSize: 30, bold: true, color: "#1D2A25" };

  slide3.tables.add({
    rows: 5,
    columns: 2,
    left: 72,
    top: 126,
    width: 610,
    height: 250,
    values: [
      ["Artifact", "Citation locator"],
      ["PDF", "page number"],
      ["DOCX", "heading path / paragraph"],
      ["XLSX", "sheet + range"],
      ["PPTX", "slide + title"],
    ],
  });

  const note = slide3.shapes.add({
    geometry: "roundRect",
    position: { left: 730, top: 150, width: 390, height: 220 },
    fill: { type: "solid", color: "#FFFDF8" },
    line: { style: "solid", fill: "#D4D9DE", width: 1 },
    borderRadius: "rounded-xl",
  });
  note.text =
    "Retriever note\nStructured locators make answer sources easier to audit and easier to render in the chat UI.";
  note.text.style = { fontSize: 20, color: "#33413D" };

  for (const [index, slide] of presentation.slides.items.entries()) {
    const png = await presentation.export({ slide, format: "png", scale: 1 });
    await writeBlob(
      path.join(outputDir, `sample-training-deck-slide-${String(index + 1).padStart(2, "0")}.png`),
      png,
    );
  }

  const pptx = await PresentationFile.exportPptx(presentation);
  await pptx.save(path.join(outputDir, "sample-training-deck.pptx"));
}

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  await buildWorkbook();
  await buildPresentation();
  console.log("Generated sample-budget.xlsx and sample-training-deck.pptx");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
