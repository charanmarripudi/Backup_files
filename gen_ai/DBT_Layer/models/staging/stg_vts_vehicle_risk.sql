-- Snapshot Vehicle Risk
WITH raw AS (
    SELECT * FROM {{ source('raw_vts', 'tt_risk_score') }}
)

SELECT
    tt_number AS vehicle_id,
    risk_score AS current_risk_score,
    transporter_name,
    transporter_code,
    total_trips AS lifetime_trips
FROM raw
