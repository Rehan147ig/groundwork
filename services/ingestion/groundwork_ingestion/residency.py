ALLOWED_REGIONS = {
    "us": {"us", "us-central1", "us-east4"},
    "eu": {"eu", "europe-west1", "europe-west3"},
    "uk": {"uk", "europe-west2"},
    "in": {"in", "asia-south1"},
}


class ResidencyViolation(ValueError):
    pass


def assert_region_allowed(residency: str, region: str) -> None:
    normalized_residency = residency.lower()
    normalized_region = normalize_region(region)
    allowed = ALLOWED_REGIONS.get(normalized_residency)
    if not allowed or normalized_region not in allowed:
        raise ResidencyViolation(f"region {region} is not allowed for {residency} residency")


def residency_from_region(region: str) -> str:
    normalized_region = normalize_region(region)
    for residency, regions in ALLOWED_REGIONS.items():
        if normalized_region in regions:
            return residency
    raise ResidencyViolation(f"unknown region {region}")


def normalize_region(region: str) -> str:
    return region.strip().lower()
