-- Cleaned Ongoing Trips
WITH raw AS (
    SELECT * FROM "hpcl_ceg"."public"."vts_ongoing_trips"
)

SELECT
    id AS trip_pk,
    trip_id,
    tt_number AS vehicle_id,
    driver_name,
    transporter_name,
    invoice_no,
    vehicle_location AS current_location,
    violation_type AS current_violation_code,
    event_start_datetime AS trip_started_at,
    event_end_datetime AS trip_ended_at,
    region,
    zone,
    bu,
    sap_id
FROM raw