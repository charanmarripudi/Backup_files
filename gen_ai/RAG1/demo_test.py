"""
QUICK DEMO TEST - Run before presentation
Tests critical edge cases and compares model performance
"""
import sys
import os
import time
import asyncio
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', '..')))

from sql_generator import ProductionSQLGenerator
import re
import training_data

MODELS_TO_TEST = ["gpt-4o-mini", "gpt-4o"]

CRITICAL_TESTS = [
    # 1. Operational Efficiency (Delay Analysis)
    "Which ongoing trips started more than 4 hours late?",
    
    # 2. Risk Management (Regional High Risk)
    "Are there any vehicles operating in the Mumbai region that have a risk score above 90?",
    
    # 3. Vendor Performance (Safety Violations)
    "Which transporters have the most speed violations in the last month?",
    
    # 4. Complex Behavior Analysis (Multiple Violations)
    "List vehicles that had both a speed violation and a route deviation in the last week",
    
    # 5. Compliance & Security (Blacklist Check)
    "Are any blacklisted vehicles currently assigned to an ongoing trip?",
    
    # 6. Anomaly Detection (High Risk, No Recent Data)
    "Show me vehicles with a high risk score that haven't reported any violations in the last 3 days",
    
    # 7. Business Impact (Cost Estimation)
    "What is the estimated cost of violations this month if each violation costs 500 rupees?",

    # 8. RAG Generalization Test (Proves Intelligence)
    # This phrasing is NOT in training data. RAG must find the "safe" concept and LLM must apply it.
    "Which transporters have the safest fleet?",

    # 9. Technical Edge Case (Text Date Casting + Row Limit)
    "Risk score for a specific vehicle last month",

    # 10. Risk Comparison (Multi-Table Logic)
    "Compare vehicle risk vs transporter risk"
]

NEW_SCHEMA_TESTS = [
    # 11. JSONB Data Query (alerts.raw_data)
    "Show alerts where the raw data has a battery voltage less than 5",
    
    # 12. Trip Audit Logic (Double Failure: Swipe In AND Swipe Out)
    "Which vehicles failed both swipe-in and swipe-out on the same trip?",
    
    # 13. Long Duration Trips (vts_ongoing_trips calculation)
    "List ongoing trips that have been running for over 48 hours",
    
    # 14. Master Data Aggregation (vts_truck_master)
    "What is the total truck capacity grouped by ownership type?",
    
    # 15. Alert Workflow Analysis (alerts.assigned_to)
    "Who are the top 3 users who have assigned the most alerts in the last 7 days?",
    
    # 16. Cross-Table Status (Risk + Live)
    "Show me vehicles with a high risk score (>80) that are currently on a trip",
    
    # 17. Negative Logic (Transporter Compliance)
    "Which transporters have had zero critical alerts in the last 30 days?",
    
    # 18. Comparative Analysis (Capacity vs Risk)
    "Compare the average risk score of vehicles with capacity greater than 20000 versus those with less than 20000",
    
    # 19. Risk Component Comparison (tt_risk_score columns)
    "Find vehicles where the harsh braking risk component is higher than the speed violation risk component",
    
    # 20. Trip Efficiency (Early Starts)
    "List trips where the actual start time was earlier than the scheduled start time",
    
    # 21. Response Time Efficiency (alerts timestamps)
    "What is the average time taken between R1 and R2 responses for Critical alerts?",
    
    # 22. E-lock Security Analysis (vts_tripauditmaster)
    "Count the number of emlock trips that had a swipe out failure, grouped by zone"
]

FULL_COVERAGE_TESTS = [
    # --- Alerts Table (Interlocks, Response Time, Supply Chain, RO Status) ---
    "Show me alerts related to 'Device Offline' interlock in the last week",
    "List alerts where R2 response time exceeded 2 hours",
    "Show me alerts for product code 'P001'",
    "Find alerts that occurred during the dry out period",
    "List locations that are currently marked as RO Offline",
    
    # --- VTS Alert History (Base Zone, Approvals, Auto-Unblock) ---
    "Which vehicles had violations outside their base zone?",
    "Who approved the most alert closures last month?",
    "List alerts that were auto-unblocked",
    
    # --- VTS Truck Master (Specs) ---
    "List vehicles with more than 4 compartments",
    "Show me vehicles with empty weight less than 5000",
    
    # --- VTS Trip Audit Master (Locks, Swipe In) ---
    "Show me trip audit records for lock ID 'L12345'",
    "How many emlock trips had swipe in failures today?",
    
    # --- Risk Scores (Components, Trends) ---
    "Which vehicles have a High device removed score but a Low overall risk score?",
    "Show the risk score trend for vehicle MH01AB1234 over the last 6 months",
    
    # --- Ongoing Trips (Driver, Route) ---
    "Who is the driver of vehicle KA01X1234 right now?",
    "Which routes have the most violations?"
]

UNSEEN_TESTS = [
    # 1. Master Data Aggregation (Capacity of Blacklisted)
    "What is the total capacity of all blacklisted vehicles?",

    # 2. Complex Time Logic (Response Time Filtering)
    "List alerts where the R1 response time was greater than 30 minutes",

    # 3. Transporter Performance (Average Delay Calculation)
    "Which transporter has the highest average delay for currently ongoing trips?",

    # 4. Driver Analysis (Linking Drivers to Violations)
    "List drivers who have had night driving violations in the last 7 days",

    # 5. Data Quality (Missing Fields)
    "Count the number of ongoing trips where the destination code is missing",

    # 6. Risk vs Actuals Anomaly (Specific Component Mismatch)
    "Show vehicles with a high speed violation risk score (> 80) but zero actual speed violations in the last month",

    # 7. Alert Trend (Specific Severity)
    "Show the daily trend of Critical alerts for the last 10 days"
]

INTENT_ROBUSTNESS_TESTS = [
    # 1. False Positive Ranking (Word Boundary Check)
    # "stoppage" contains "top". Before fix: Classified as RANKING (needs ORDER BY). After fix: LIST/FILTER.
    "Show me vehicles with stoppage violations", 
    
    # 2. Snapshot vs History (Semantic Validation)
    # Risk tables are snapshots. Trend queries must use history table.
    "Show me the trend of vehicle risk scores for the last 6 months",
    
    # 3. Alert Status Source (Semantic Validation)
    # 'Open' status is only in 'alerts' table, not 'vts_alert_history'.
    "Which vehicles have Open alerts?",
    
    # 4. Driver Name Source (Semantic Validation)
    # Driver name is only in 'vts_ongoing_trips'.
    "Who is the driver for vehicle KA01 with high risk?"
]

COMPLEX_CLIENT_TESTS = [
    # 1. Complex Risk & Operations (Risk + Audit + Aggregation)
    # Tests if the system can link Transporter Risk (Score) with Audit Performance (Swipe Failures)
    "Which transporters have a fleet risk score above 50 but have zero swipe-out failures in the last week?",

    # 2. Geospatial Anomaly (Count Distinct Zone + Filter)
    # Tests "Roaming Offender" logic - vehicles failing in multiple regions
    "Find vehicles that have reported 'Device Offline' violations in more than 3 different zones in the last month",

    # 3. Operational Efficiency (Date arithmetic + Grouping)
    # Tests calculation of delays (Actual - Scheduled) aggregated by Transporter
    "Calculate the average delay between scheduled start and actual trip start for each transporter, ordered by longest delay",

    # 4. Alert Fatigue (High volume filtering)
    # Tests ability to handle high-frequency event detection
    "Identify vehicles that have generated more than 20 'Low' severity alerts in the last 24 hours",

    # 5. Cross-Table Integrity (Exclusion join)
    # Tests data consistency between Audit logs and Live Trips
    "List invoices present in vts_tripauditmaster but missing from vts_ongoing_trips for trips created today",
    
    # 6. Multi-Factor Risk Analysis (Risk Score + Multiple Violation Types)
    # Tests 3-way condition: High Risk Score AND Violation A AND Violation B
    "Show me vehicles with a risk score > 90 that have also had both 'Speed Violation' and 'Night Driving' in the last 3 days",
    
    # 7. Maintenance Correlation (Alerts + Master Status)
    # Tests if blacklisted vehicles are still generating hardware alerts
    "Which vehicles have 'Power Disconnect' alerts and are currently marked as 'Blacklisted' in the master table?"
]

ALL_TESTS = CRITICAL_TESTS + NEW_SCHEMA_TESTS + FULL_COVERAGE_TESTS + UNSEEN_TESTS + INTENT_ROBUSTNESS_TESTS + COMPLEX_CLIENT_TESTS

async def quick_test():
    """Run quick validation and model comparison"""
    print("\nQUICK DEMO VALIDATION & MODEL COMPARISON")
    print("="*80)
    
    # Set force_reindex_rag=False for the actual demo to avoid startup delay
    generator = ProductionSQLGenerator(training_data, force_reindex_rag=False)
    
    results = {}
    
    for i, question in enumerate(ALL_TESTS, 1):
        print(f"\n[{i}/{len(ALL_TESTS)}] Question: {question}")
        print("-" * 80)

        try:
            result = generator.generate_sql(
                question,
                use_cache=False,  
                strategy="tournament"
            )

            start_time = time.time()
            duration = time.time() - start_time
            sql = result.get('sql_query', '')
            success = result.get('success', False)
            error_msg = result.get('answer') if not success else None
            validation_log = result.get('validation_log', [])
            source_model = result.get('source', 'N/A')

            results[question] = {
                'success': success,
                'sql': sql,
                'error': error_msg,
                'duration': duration,
                'data': result.get('data', []) if success else [],
                'source_model': source_model
            }

            print(f"  Result: {'Success' if success else 'Failure'} (Duration: {duration:.2f} seconds)")
            print(f"  Winning Model: {source_model}")
            print(f"  SQL Query: {sql}")
            if validation_log:
                print("  --- Tournament Log ---")
                for log_entry in validation_log:
                    print(f"    {log_entry}")
                print("  ----------------------")

        except Exception as e:
            print(f"  ERROR: An exception occurred during tournament generation: {e}")
            results[question] = {'success': False, 'sql': '', 'error': str(e), 'duration': 0, 'data': [], 'source_model': 'N/A'}

    print("\n" + "="*80)
    print("PERFORMANCE SUMMARY")
    print("="*80)
    print(f"{'Question':<50} | {'Winning Model':<40} | {'Status':<8} | {'Time (s)':<10}")
    print("-" * 115)
    
    overall_success = True
    for q in ALL_TESTS:
        short_q = (q[:47] + '...') if len(q) > 47 else q
        res = results.get(q, {})
        status = "PASS" if res.get('success') else "FAIL"
        time_str = f"{res.get('duration', 0):.2f}"
        model_str = res.get('source_model', 'N/A')
        print(f"{short_q:<50} | {model_str:<40} | {status:<8} | {time_str:<10}")
        if not res.get('success'):
            overall_success = False
    print("-" * 115)

    return overall_success
                
if __name__ == "__main__":
    success = asyncio.run(quick_test())
    sys.exit(0 if success else 1)
