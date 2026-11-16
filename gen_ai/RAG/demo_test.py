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

MODELS_TO_TEST = ["mannix/defog-llama3-sqlcoder-8b:latest"]

CRITICAL_TESTS = [
    "Average risk score of transporters who have vehicles with capacity > 20000",
    "List vehicles currently on a trip that have an open critical alert",
    "Vehicles with speed violations on weekends in the last month",
    "Transporters with no vehicles having risk score greater than 90",
    "Show me the worst performing trucks in the North zone based on safety",
    "Vehicles with 'Power Disconnect' violation in 'Mumbai' followed by 'Device Offline' within 1 hour"
]

async def quick_test():
    """Run quick validation and model comparison"""
    print("\nQUICK DEMO VALIDATION & MODEL COMPARISON")
    print("="*80)
    
    # Set force_reindex_rag=False for the actual demo to avoid startup delay
    generator = ProductionSQLGenerator(training_data, force_reindex_rag=True)
    
    results = {}
    
    for i, question in enumerate(CRITICAL_TESTS, 1):
        print(f"\n[{i}/{len(CRITICAL_TESTS)}] Question: {question}")
        print("-" * 80)

        try:
            result = await generator.generate_sql(
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
    for q in CRITICAL_TESTS:
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