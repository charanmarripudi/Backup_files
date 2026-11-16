-- Cleaned Violation History
WITH raw AS (
    SELECT * FROM {{ source('raw_vts', 'vts_alert_history') }}
),

truck_master AS (
    SELECT vehicle_id, transporter_name, transporter_code
    FROM {{ ref('stg_vts_truck_master') }}
)

SELECT
    r.id AS event_id,
    r.tl_number AS vehicle_id,
    tm.transporter_name,
    tm.transporter_code,
    r.vts_end_datetime AS event_at,
    r.total_trips,
    -- Pre-calculate the total violations metric
    (COALESCE(r.stoppage_violations_count, 0) + 
     COALESCE(r.route_deviation_count, 0) + 
     COALESCE(r.speed_violation_count, 0) + 
     COALESCE(r.night_driving_count, 0) + 
     COALESCE(r.device_offline_count, 0) + 
     COALESCE(r.device_tamper_count, 0) + 
     COALESCE(r.continuous_driving_count, 0) + 
     COALESCE(r.no_halt_zone_count, 0) + 
     COALESCE(r.main_supply_removal_count, 0)) AS total_violations_count,
    r.stoppage_violations_count,
    r.route_deviation_count,
    r.speed_violation_count,
    r.night_driving_count,
    r.violation_type AS violation_tags, -- original array
    r.region,
    r.bu,
    r.zone,
    r.location_name
FROM raw r
LEFT JOIN truck_master tm ON r.tl_number = tm.vehicle_id
