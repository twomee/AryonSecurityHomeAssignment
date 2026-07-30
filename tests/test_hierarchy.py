import os

import requests

BASE_URL = os.getenv("CLOUDSTORE_BASE_URL", "http://localhost:8080")


def post_hierarchy(hierarchy):
    return requests.post(f"{BASE_URL}/hierarchy", json=hierarchy, timeout=10)


def get_hierarchy(node_id):
    return requests.get(f"{BASE_URL}/hierarchy/{node_id}", timeout=10)


def test_different_roots_remain_independent():
    first = {
        "id": 900001,
        "type": "management_group",
        "children": [
            {"id": 900002, "type": "subscription", "children": []},
        ],
    }
    second = {
        "id": 910001,
        "type": "management_group",
        "children": [],
    }

    assert post_hierarchy(first).status_code == 200
    assert post_hierarchy(second).status_code == 200
    assert get_hierarchy(first["id"]).json() == first
    assert get_hierarchy(second["id"]).json() == second


def test_get_internal_node_returns_only_ordered_subtree():
    hierarchy = {
        "id": 920001,
        "type": "management_group",
        "children": [
            {
                "id": 920002,
                "type": "subscription",
                "children": [
                    {"id": 920003, "type": "resource_group", "children": []},
                    {"id": 920004, "type": "resource_group", "children": []},
                ],
            }
        ],
    }

    assert post_hierarchy(hierarchy).status_code == 200

    response = get_hierarchy(920002)
    assert response.status_code == 200
    assert response.json() == hierarchy["children"][0]


def test_removed_node_is_no_longer_retrievable():
    before = {
        "id": 930001,
        "type": "management_group",
        "children": [
            {"id": 930002, "type": "subscription", "children": []},
        ],
    }
    after = {
        "id": 930001,
        "type": "management_group",
        "children": [],
    }

    assert post_hierarchy(before).status_code == 200
    assert post_hierarchy(after).status_code == 200
    assert get_hierarchy(930002).status_code == 404
    assert get_hierarchy(930001).json() == after


def test_duplicate_ids_are_rejected_without_partial_write():
    invalid = {
        "id": 940001,
        "type": "management_group",
        "children": [
            {"id": 940002, "type": "subscription", "children": []},
            {"id": 940002, "type": "resource_group", "children": []},
        ],
    }

    response = post_hierarchy(invalid)

    assert response.status_code == 422
    assert get_hierarchy(940001).status_code == 404


def test_unknown_json_fields_are_rejected():
    response = requests.post(
        f"{BASE_URL}/hierarchy",
        json={
            "id": 950001,
            "type": "management_group",
            "children": [],
            "unexpected": True,
        },
        timeout=10,
    )

    assert response.status_code == 400
