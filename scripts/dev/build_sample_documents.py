from __future__ import annotations

from pathlib import Path

from docx import Document
from docx.enum.section import WD_SECTION
from docx.enum.style import WD_STYLE_TYPE
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.oxml import OxmlElement
from docx.oxml.ns import qn
from docx.shared import Inches, Pt, RGBColor
from reportlab.lib import colors
from reportlab.lib.pagesizes import LETTER
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import inch
from reportlab.platypus import (
    Paragraph,
    SimpleDocTemplate,
    Spacer,
    Table,
    TableStyle,
)


ROOT = Path(__file__).resolve().parents[2]
OUTPUT_DIR = ROOT / "data" / "sample-docs"
DOCX_PATH = OUTPUT_DIR / "sample-handbook.docx"
PDF_PATH = OUTPUT_DIR / "sample-policy.pdf"


TITLE_BLUE = RGBColor(27, 62, 94)
TEXT_GRAY = RGBColor(74, 85, 104)
LIGHT_FILL = "EAF1F7"


def ensure_dir() -> None:
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)


def set_run_font(run, *, name: str = "Calibri", size: int = 11, color: RGBColor | None = None, bold: bool | None = None) -> None:
    run.font.name = name
    run._element.rPr.rFonts.set(qn("w:ascii"), name)
    run._element.rPr.rFonts.set(qn("w:hAnsi"), name)
    run.font.size = Pt(size)
    if color is not None:
        run.font.color.rgb = color
    if bold is not None:
        run.bold = bold


def add_shading(cell, fill: str) -> None:
    tc_pr = cell._tc.get_or_add_tcPr()
    shd = OxmlElement("w:shd")
    shd.set(qn("w:fill"), fill)
    tc_pr.append(shd)


def build_docx() -> None:
    doc = Document()
    section = doc.sections[0]
    section.start_type = WD_SECTION.NEW_PAGE
    section.top_margin = Inches(1)
    section.bottom_margin = Inches(1)
    section.left_margin = Inches(1)
    section.right_margin = Inches(1)

    normal = doc.styles["Normal"]
    normal.font.name = "Calibri"
    normal.font.size = Pt(11)

    if "Body Small" not in doc.styles:
        style = doc.styles.add_style("Body Small", WD_STYLE_TYPE.PARAGRAPH)
        style.font.name = "Calibri"
        style.font.size = Pt(10)

    title = doc.add_paragraph()
    title.alignment = WD_ALIGN_PARAGRAPH.LEFT
    title.paragraph_format.space_after = Pt(4)
    title_run = title.add_run("Atlas Operations Handbook Sample")
    set_run_font(title_run, size=22, color=TITLE_BLUE, bold=True)

    subtitle = doc.add_paragraph()
    subtitle.paragraph_format.space_after = Pt(14)
    subtitle_run = subtitle.add_run("Internal reference sample for knowledge ingestion, citation testing, and retrieval evaluation")
    set_run_font(subtitle_run, size=11, color=TEXT_GRAY)

    intro = doc.add_paragraph()
    intro.paragraph_format.space_after = Pt(10)
    intro.add_run(
        "This sample document intentionally mixes prose, lists, and a compact table so the ingestion pipeline can preserve headings, bullets, and structured locator metadata."
    )

    sections = [
        (
            "1. Leave And Time Off",
            [
                "Annual leave requests must be submitted at least 3 business days in advance.",
                "Requests longer than 5 consecutive business days require both direct-manager approval and HR confirmation.",
                "Managers should confirm coverage expectations in the same approval thread.",
            ],
        ),
        (
            "2. Expense Reimbursement",
            [
                "Travel reimbursements must be filed within 10 calendar days after the trip ends.",
                "Any single hotel line item above CNY 800 must include a short business justification.",
                "Receipts are mandatory unless finance leadership grants an exception in writing.",
            ],
        ),
    ]

    for heading_text, bullets in sections:
        heading = doc.add_paragraph()
        heading.paragraph_format.space_before = Pt(14)
        heading.paragraph_format.space_after = Pt(6)
        heading_run = heading.add_run(heading_text)
        set_run_font(heading_run, size=14, color=TITLE_BLUE, bold=True)

        for bullet in bullets:
            p = doc.add_paragraph(style="List Bullet")
            p.paragraph_format.space_after = Pt(3)
            p.add_run(bullet)

    h3 = doc.add_paragraph()
    h3.paragraph_format.space_before = Pt(14)
    h3.paragraph_format.space_after = Pt(6)
    h3_run = h3.add_run("3. Citation Locator Reference")
    set_run_font(h3_run, size=14, color=TITLE_BLUE, bold=True)

    table = doc.add_table(rows=1, cols=2)
    table.style = "Table Grid"
    table.autofit = False
    widths = [Inches(2.0), Inches(4.1)]
    headers = ["Artifact", "Locator guidance"]
    for idx, width in enumerate(widths):
        table.columns[idx].width = width
    for idx, text in enumerate(headers):
        cell = table.rows[0].cells[idx]
        cell.width = widths[idx]
        add_shading(cell, LIGHT_FILL)
        p = cell.paragraphs[0]
        p.paragraph_format.space_after = Pt(0)
        run = p.add_run(text)
        set_run_font(run, size=10, color=TITLE_BLUE, bold=True)

    rows = [
        ("PDF", "Page number. Use a page-first citation label for policy summaries."),
        ("DOCX", "Heading path. Retain the nearest heading and paragraph block for grounded answers."),
        ("XLSX", "Sheet + range. Promote sheet name and table/range info into source_locator metadata."),
        ("PPTX", "Slide number. Return slide number and title together for citation display."),
    ]
    for values in rows:
        cells = table.add_row().cells
        for idx, value in enumerate(values):
            cells[idx].width = widths[idx]
            p = cells[idx].paragraphs[0]
            p.paragraph_format.space_after = Pt(0)
            run = p.add_run(value)
            set_run_font(run, size=10)

    note = doc.add_paragraph(style="Body Small")
    note.paragraph_format.space_before = Pt(8)
    note_run = note.add_run(
        "Sample generation note: this document is optimized for structured ingestion rather than full visual branding."
    )
    set_run_font(note_run, size=10, color=TEXT_GRAY)

    doc.save(DOCX_PATH)


def build_pdf() -> None:
    styles = getSampleStyleSheet()
    title_style = ParagraphStyle(
        "AtlasTitle",
        parent=styles["Title"],
        fontName="Helvetica-Bold",
        fontSize=22,
        textColor=colors.HexColor("#1B3E5E"),
        spaceAfter=8,
        alignment=0,
    )
    body_style = ParagraphStyle(
        "AtlasBody",
        parent=styles["BodyText"],
        fontName="Helvetica",
        fontSize=10.5,
        leading=14,
        spaceAfter=6,
    )
    heading_style = ParagraphStyle(
        "AtlasHeading",
        parent=styles["Heading2"],
        fontName="Helvetica-Bold",
        fontSize=13,
        textColor=colors.HexColor("#1B3E5E"),
        spaceBefore=10,
        spaceAfter=4,
    )

    doc = SimpleDocTemplate(
        str(PDF_PATH),
        pagesize=LETTER,
        rightMargin=0.9 * inch,
        leftMargin=0.9 * inch,
        topMargin=0.85 * inch,
        bottomMargin=0.75 * inch,
    )

    story = [
        Paragraph("Atlas Policy PDF Sample", title_style),
        Paragraph(
            "Reference document for PDF parsing, page-number citation checks, and mixed prose/table ingestion.",
            body_style,
        ),
        Spacer(1, 6),
        Paragraph("Leave Policy", heading_style),
        Paragraph("Annual leave requires submission at least 3 business days before the first day away.", body_style),
        Paragraph("Leave longer than 5 consecutive business days requires both manager and HR approval.", body_style),
        Paragraph("Expense Reimbursement", heading_style),
        Paragraph("Travel reimbursement claims must be filed within 10 calendar days after the trip ends.", body_style),
        Paragraph("Hotel charges above CNY 800 need a short business justification attached to the claim.", body_style),
        Spacer(1, 8),
    ]

    table = Table(
        [
            ["Document type", "Default locator", "Fallback handling"],
            ["txt / md", "Paragraph block", "Use nearby heading when available."],
            ["pdf", "Page number", "Run OCR fallback only when text extraction is sparse."],
            ["xlsx", "Sheet + range", "Preserve numeric context and header row."],
            ["pptx", "Slide number", "Pair slide title with slide number in answer sources."],
        ],
        colWidths=[1.35 * inch, 1.55 * inch, 3.2 * inch],
        hAlign="LEFT",
    )
    table.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#EAF1F7")),
                ("TEXTCOLOR", (0, 0), (-1, 0), colors.HexColor("#1B3E5E")),
                ("FONTNAME", (0, 0), (-1, 0), "Helvetica-Bold"),
                ("FONTNAME", (0, 1), (-1, -1), "Helvetica"),
                ("FONTSIZE", (0, 0), (-1, -1), 9),
                ("GRID", (0, 0), (-1, -1), 0.5, colors.HexColor("#B8C5D1")),
                ("VALIGN", (0, 0), (-1, -1), "TOP"),
                ("LEFTPADDING", (0, 0), (-1, -1), 6),
                ("RIGHTPADDING", (0, 0), (-1, -1), 6),
                ("TOPPADDING", (0, 0), (-1, -1), 5),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 5),
            ]
        )
    )
    story.append(table)
    story.append(Spacer(1, 10))
    story.append(
        Paragraph(
            "Sample note: this PDF is intentionally compact but includes headings, paragraphs, and a locator matrix to exercise retrieval extraction.",
            body_style,
        )
    )

    doc.build(story)


def main() -> None:
    ensure_dir()
    build_docx()
    build_pdf()
    print(f"Generated {DOCX_PATH}")
    print(f"Generated {PDF_PATH}")


if __name__ == "__main__":
    main()
