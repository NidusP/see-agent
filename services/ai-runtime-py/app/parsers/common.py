from __future__ import annotations

import base64
import json
import re
import zlib
from collections import defaultdict
from pathlib import Path
from typing import Any
from xml.etree import ElementTree
from zipfile import ZipFile


def read_text_file(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def split_paragraphs(text: str) -> list[str]:
    parts = [part.strip() for part in re.split(r"\n\s*\n", text) if part.strip()]
    if parts:
        return parts
    return [line.strip() for line in text.splitlines() if line.strip()]


def strip_inline_markdown(text: str) -> str:
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"\[(.*?)\]\((.*?)\)", r"\1", text)
    return text.strip()


def make_document_id(file_id: str, source_format: str) -> str:
    return f"doc:{file_id}:{source_format}"


def count_tokens(text: str) -> int:
    return max(1, len(re.findall(r"\S+", text)))


def column_index_to_name(index: int) -> str:
    name = ""
    value = index
    while value > 0:
        value, remainder = divmod(value - 1, 26)
        name = chr(65 + remainder) + name
    return name


def column_name_to_index(name: str) -> int:
    value = 0
    for char in name:
        value = value * 26 + (ord(char.upper()) - 64)
    return value


def parse_cell_reference(reference: str) -> tuple[int, int]:
    match = re.fullmatch(r"([A-Z]+)(\d+)", reference.upper())
    if not match:
        raise ValueError(f"Unsupported cell reference: {reference}")
    return int(match.group(2)), column_name_to_index(match.group(1))


def normalize_whitespace(text: str) -> str:
    return re.sub(r"\s+", " ", text).strip()


def read_xml_from_zip(path: Path, member: str) -> ElementTree.Element:
    with ZipFile(path) as archive:
        return ElementTree.fromstring(archive.read(member))


def load_zip_bytes(path: Path, member: str) -> bytes:
    with ZipFile(path) as archive:
        return archive.read(member)


def local_name(tag: str) -> str:
    if "}" in tag:
        return tag.rsplit("}", 1)[1]
    return tag


def iter_text_nodes(element: ElementTree.Element, tag_suffix: str) -> list[str]:
    values: list[str] = []
    for node in element.iter():
        if local_name(node.tag) == tag_suffix and node.text:
            values.append(node.text)
    return values


def parse_ooxml_relationships(path: Path, member: str, base_dir: str) -> dict[str, str]:
    root = read_xml_from_zip(path, member)
    relationships: dict[str, str] = {}
    for rel in root:
        rel_id = rel.attrib.get("Id")
        target = rel.attrib.get("Target")
        if not rel_id or not target:
            continue
        target_path = target if target.startswith("/") else f"{base_dir}/{target}".replace("//", "/")
        relationships[rel_id] = target_path.lstrip("/")
    return relationships


def load_shared_strings(path: Path) -> list[str]:
    try:
        root = read_xml_from_zip(path, "xl/sharedStrings.xml")
    except KeyError:
        return []

    values: list[str] = []
    for item in root:
        texts = iter_text_nodes(item, "t")
        values.append("".join(texts))
    return values


def flatten_json(value: Any, prefix: str = "$") -> list[tuple[str, str]]:
    flattened: list[tuple[str, str]] = []
    if isinstance(value, dict):
        for key, item in value.items():
            child_prefix = f"{prefix}.{key}"
            flattened.extend(flatten_json(item, child_prefix))
        return flattened
    if isinstance(value, list):
        for index, item in enumerate(value):
            child_prefix = f"{prefix}[{index}]"
            flattened.extend(flatten_json(item, child_prefix))
        return flattened

    flattened.append((prefix, json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value))
    return flattened


def parse_pdf_objects(raw: bytes) -> dict[int, bytes]:
    objects: dict[int, bytes] = {}
    for match in re.finditer(rb"(\d+)\s+\d+\s+obj(.*?)endobj", raw, re.S):
        objects[int(match.group(1))] = match.group(2)
    return objects


def decode_pdf_stream(stream_body: bytes, object_body: bytes) -> bytes:
    payload = stream_body.rstrip(b"\r\n")
    decoded = payload

    filters: list[str] = []
    array_match = re.search(rb"/Filter\s*\[(.*?)\]", object_body, re.S)
    if array_match:
        filters = re.findall(rb"/([A-Za-z0-9]+)", array_match.group(1).decode("latin1").encode("latin1"))
        filters = [flt.decode("latin1") for flt in filters]
    else:
        single_match = re.search(rb"/Filter\s*/([A-Za-z0-9]+)", object_body)
        if single_match:
            filters = [single_match.group(1).decode("latin1")]

    for filter_name in filters:
        if filter_name == "ASCII85Decode":
            decoded = base64.a85decode(decoded, adobe=True)
        elif filter_name == "FlateDecode":
            decoded = zlib.decompress(decoded)

    return decoded


def parse_pdf_literal_string(raw: bytes) -> str:
    chars: list[str] = []
    index = 0
    while index < len(raw):
        current = raw[index]
        if current == 92 and index + 1 < len(raw):
            next_byte = raw[index + 1]
            chars.append(chr(next_byte))
            index += 2
            continue
        chars.append(chr(current))
        index += 1
    return normalize_whitespace("".join(chars))


def extract_pdf_text(decoded_stream: bytes) -> list[str]:
    texts: list[str] = []
    for match in re.finditer(rb"\((?:\\.|[^\\()])*\)\s*Tj", decoded_stream):
        literal = match.group(0).rsplit(b")", 1)[0][1:]
        text = parse_pdf_literal_string(literal)
        if text:
            texts.append(text)

    for match in re.finditer(rb"\[(.*?)\]\s*TJ", decoded_stream, re.S):
        nested = re.findall(rb"\((?:\\.|[^\\()])*\)", match.group(1))
        parts = [parse_pdf_literal_string(item[1:-1]) for item in nested]
        text = normalize_whitespace("".join(parts))
        if text:
            texts.append(text)

    deduped: list[str] = []
    for text in texts:
        if not deduped or deduped[-1] != text:
            deduped.append(text)
    return deduped


def extract_pdf_pages(path: Path) -> list[list[str]]:
    raw = path.read_bytes()
    objects = parse_pdf_objects(raw)
    page_object_ids = [
        object_id
        for object_id, body in objects.items()
        if b"/Type /Page" in body and b"/Type /Pages" not in body
    ]
    pages: list[list[str]] = []
    for object_id in page_object_ids:
        body = objects[object_id]
        content_refs = [int(ref) for ref in re.findall(rb"/Contents\s+(\d+)\s+\d+\s+R", body)]
        page_text: list[str] = []
        for ref in content_refs:
            content_body = objects.get(ref, b"")
            match = re.search(rb"stream\s*(.*?)endstream", content_body, re.S)
            if not match:
                continue
            decoded = decode_pdf_stream(match.group(1), content_body)
            page_text.extend(extract_pdf_text(decoded))
        pages.append(page_text)
    return pages


def group_rows_into_regions(rows: dict[int, dict[int, str]]) -> list[dict[str, Any]]:
    if not rows:
        return []

    regions: list[dict[str, Any]] = []
    active_rows: list[int] = []

    def finalize_region() -> None:
        if not active_rows:
            return
        min_row = min(active_rows)
        max_row = max(active_rows)
        min_col = min(col for row_idx in active_rows for col in rows[row_idx])
        max_col = max(col for row_idx in active_rows for col in rows[row_idx])

        region_rows: list[list[str]] = []
        for row_idx in range(min_row, max_row + 1):
            row_values = []
            for col_idx in range(min_col, max_col + 1):
                row_values.append(rows.get(row_idx, {}).get(col_idx, ""))
            region_rows.append(row_values)

        regions.append(
            {
                "start_row": min_row,
                "end_row": max_row,
                "start_col": min_col,
                "end_col": max_col,
                "rows": region_rows,
                "cell_range": f"{column_index_to_name(min_col)}{min_row}:{column_index_to_name(max_col)}{max_row}",
            }
        )
        active_rows.clear()

    previous_row = None
    for row_idx in sorted(rows):
        if previous_row is None or row_idx == previous_row + 1:
            active_rows.append(row_idx)
        else:
            finalize_region()
            active_rows.append(row_idx)
        previous_row = row_idx

    finalize_region()
    return regions


def parse_xlsx_workbook(path: Path) -> list[dict[str, Any]]:
    workbook_root = read_xml_from_zip(path, "xl/workbook.xml")
    rels = parse_ooxml_relationships(path, "xl/_rels/workbook.xml.rels", "xl")
    shared_strings = load_shared_strings(path)

    sheets: list[dict[str, Any]] = []
    with ZipFile(path) as archive:
        for sheet in workbook_root.iter():
            if local_name(sheet.tag) != "sheet":
                continue
            rel_id = sheet.attrib.get("{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id")
            if not rel_id or rel_id not in rels:
                continue
            member = rels[rel_id]
            sheet_root = ElementTree.fromstring(archive.read(member))
            rows: dict[int, dict[int, str]] = defaultdict(dict)
            for cell in sheet_root.iter():
                if local_name(cell.tag) != "c":
                    continue
                cell_ref = cell.attrib.get("r")
                if not cell_ref:
                    continue
                row_idx, col_idx = parse_cell_reference(cell_ref)
                cell_type = cell.attrib.get("t")
                value_node = next((node for node in cell if local_name(node.tag) == "v"), None)
                inline_node = next((node for node in cell if local_name(node.tag) == "is"), None)
                value = ""
                if cell_type == "s" and value_node is not None and value_node.text is not None:
                    value = shared_strings[int(value_node.text)]
                elif inline_node is not None:
                    value = "".join(iter_text_nodes(inline_node, "t"))
                elif value_node is not None and value_node.text is not None:
                    value = value_node.text
                value = normalize_whitespace(value)
                if value:
                    rows[row_idx][col_idx] = value

            regions = group_rows_into_regions(rows)
            sheets.append(
                {
                    "name": sheet.attrib.get("name", "Sheet"),
                    "regions": regions,
                }
            )
    return sheets


def parse_pptx_slides(path: Path, include_notes: bool = False) -> list[dict[str, Any]]:
    presentation_root = read_xml_from_zip(path, "ppt/presentation.xml")
    rels = parse_ooxml_relationships(path, "ppt/_rels/presentation.xml.rels", "ppt")
    slide_targets: list[str] = []
    for node in presentation_root.iter():
        if local_name(node.tag) != "sldId":
            continue
        rel_id = node.attrib.get("{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id")
        if rel_id and rel_id in rels:
            slide_targets.append(rels[rel_id])

    slides: list[dict[str, Any]] = []
    with ZipFile(path) as archive:
        for index, member in enumerate(slide_targets, start=1):
            slide_root = ElementTree.fromstring(archive.read(member))
            text_shapes: list[str] = []
            tables: list[str] = []
            for node in slide_root.iter():
                tag = local_name(node.tag)
                if tag == "sp":
                    texts = [text for text in iter_text_nodes(node, "t") if normalize_whitespace(text)]
                    if texts:
                        text_shapes.append(normalize_whitespace(" ".join(texts)))
                elif tag == "tbl":
                    rows: list[str] = []
                    for row in node:
                        if local_name(row.tag) != "tr":
                            continue
                        cells = [normalize_whitespace(" ".join(iter_text_nodes(cell, "t"))) for cell in row if local_name(cell.tag) == "tc"]
                        row_text = " | ".join(cell for cell in cells if cell)
                        if row_text:
                            rows.append(row_text)
                    if rows:
                        tables.append("\n".join(rows))

            notes_text = ""
            notes_member = f"ppt/notesSlides/notesSlide{index}.xml"
            if include_notes:
                try:
                    notes_root = ElementTree.fromstring(archive.read(notes_member))
                except KeyError:
                    notes_root = None
                if notes_root is not None:
                    notes_text = normalize_whitespace(" ".join(iter_text_nodes(notes_root, "t")))

            slides.append(
                {
                    "slide_no": index,
                    "title": text_shapes[0] if text_shapes else f"Slide {index}",
                    "text_shapes": text_shapes,
                    "tables": tables,
                    "notes": notes_text,
                }
            )
    return slides
