from __future__ import annotations

from scripts.eval.run_baseline import has_expected_locator_hit


def xlsx_record(expected_locator: str) -> dict[str, object]:
    return {
        "format_family": "xlsx",
        "expected_source_files": ["sample-budget.xlsx"],
        "expected_source_locators": [expected_locator],
    }


def xlsx_citation(
    sheet_name: str,
    cell_range: str,
    filename: str = "sample-budget.xlsx",
) -> dict[str, object]:
    return {
        "filename": filename,
        "source_locator": {
            "source_format": "xlsx",
            "sheet_name": sheet_name,
            "cell_range": cell_range,
        }
    }


def json_record(expected_locator: str) -> dict[str, object]:
    return {
        "format_family": "json",
        "expected_source_files": ["policy-catalog.json"],
        "expected_source_locators": [expected_locator],
    }


def json_citation(filename: str, json_path: str) -> dict[str, object]:
    return {
        "filename": filename,
        "source_locator": {
            "source_format": "json",
            "json_path": json_path,
        },
    }


def text_record(expected_locator: str) -> dict[str, object]:
    return {
        "format_family": "md",
        "expected_source_files": ["employee-handbook.md"],
        "expected_source_locators": [expected_locator],
    }


def text_citation(filename: str, heading_path: list[str]) -> dict[str, object]:
    return {
        "filename": filename,
        "source_locator": {"heading_path": heading_path},
    }


def test_xlsx_locator_matches_containing_region_on_same_sheet() -> None:
    assert has_expected_locator_hit(
        xlsx_record("Summary!B3"),
        [xlsx_citation("Summary", "A1:F5")],
    )


def test_xlsx_locator_matches_containing_region_for_expected_range() -> None:
    assert has_expected_locator_hit(
        xlsx_record("Timeline!A3:D3"),
        [xlsx_citation("Timeline", "A1:D5")],
    )


def test_xlsx_locator_rejects_different_sheet() -> None:
    assert not has_expected_locator_hit(
        xlsx_record("Summary!B3"),
        [xlsx_citation("Assumptions", "A1:F5")],
    )


def test_xlsx_locator_rejects_correct_range_from_wrong_filename() -> None:
    assert not has_expected_locator_hit(
        xlsx_record("Summary!B3"),
        [xlsx_citation("Summary", "A1:F5", "other-budget.xlsx")],
    )


def test_xlsx_locator_rejects_adjacent_region() -> None:
    assert not has_expected_locator_hit(
        xlsx_record("Summary!B3"),
        [xlsx_citation("Summary", "C3:F5")],
    )


def test_xlsx_locator_rejects_intersecting_region_that_does_not_contain_expected_range() -> None:
    assert not has_expected_locator_hit(
        xlsx_record("Summary!B3:D3"),
        [xlsx_citation("Summary", "A1:C4")],
    )


def test_xlsx_locator_rejects_unparseable_locator() -> None:
    assert not has_expected_locator_hit(
        xlsx_record("Summary!B3"),
        [xlsx_citation("Summary", "A1-F5")],
    )
    assert not has_expected_locator_hit(
        xlsx_record("Summary!not-a-cell"),
        [xlsx_citation("Summary", "A1:F5")],
    )


def test_json_selector_matches_travel_and_leave_policy_descendants() -> None:
    assert has_expected_locator_hit(
        json_record("$.policies[?id=travel_reimbursement].rules"),
        [json_citation("policy-catalog.json", "$.policies[1].rules[0]")],
    )
    assert has_expected_locator_hit(
        json_record("$.policies[?id=leave_policy]"),
        [json_citation("policy-catalog.json", "$.policies[0].title")],
    )


def test_json_selector_rejects_wrong_policy() -> None:
    assert not has_expected_locator_hit(
        json_record("$.policies[?id=travel_reimbursement]"),
        [json_citation("policy-catalog.json", "$.policies[0].rules[0]")],
    )


def test_json_selector_rejects_wrong_file_and_cross_citation_match() -> None:
    record = json_record("$.policies[?id=travel_reimbursement]")
    assert not has_expected_locator_hit(
        record,
        [json_citation("other-policy-catalog.json", "$.policies[1].rules[0]")],
    )
    assert not has_expected_locator_hit(
        record,
        [
            json_citation("policy-catalog.json", "$.policies[0].rules[0]"),
            json_citation("other-policy-catalog.json", "$.policies[1].rules[0]"),
        ],
    )


def test_text_locator_rejects_cross_citation_match() -> None:
    assert not has_expected_locator_hit(
        text_record("Travel Policy"),
        [
            text_citation("employee-handbook.md", ["Leave Policy"]),
            text_citation("other-handbook.md", ["Travel Policy"]),
        ],
    )
