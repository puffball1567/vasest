#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

keika_command=${KEIKA:-keika}
export GOCACHE="$test_dir/go-cache"

cd "$project_dir"
"$keika_command" deps lock
"$keika_command" check vasest.km
"$keika_command" check cmd/vasest/main.km
"$keika_command" emit-go -package vasest -o "$test_dir/vasest.go" vasest.km
cp tests/vasest_test.go "$test_dir/"
cp tests/mock_return_matchers_test.go "$test_dir/"
cp tests/mock_outcome_matchers_test.go "$test_dir/"
cp tests/mock_error_matchers_test.go "$test_dir/"
cp tests/slice_predicate_matchers_test.go "$test_dir/"
cp tests/slice_indexed_predicate_test.go "$test_dir/"
cp tests/slice_elements_test.go "$test_dir/"
cp tests/unordered_slice_matchers_test.go "$test_dir/"
cp tests/slice_contains_all_test.go "$test_dir/"
cp tests/cycle_diagnostics_test.go "$test_dir/"
cp tests/map_predicate_matchers_test.go "$test_dir/"
cp tests/map_collection_predicate_test.go "$test_dir/"
cp tests/map_entries_test.go "$test_dir/"
cp tests/string_boundary_test.go "$test_dir/"
cp tests/slice_boundary_test.go "$test_dir/"
cp tests/slice_sequence_test.go "$test_dir/"
cp tests/slice_in_order_test.go "$test_dir/"
cp tests/mock_predicate_matchers_test.go "$test_dir/"
cp tests/mock_return_predicate_matchers_test.go "$test_dir/"
cp tests/mock_thrown_predicate_matchers_test.go "$test_dir/"
cp tests/float_matchers_test.go "$test_dir/"
cp tests/wait_value_test.go "$test_dir/"
cp tests/indexed_tables_test.go "$test_dir/"
cp tests/inactive_tables_test.go "$test_dir/"
cp tests/context_hooks_test.go "$test_dir/"
cp tests/mock_reset_test.go "$test_dir/"
cp tests/function_throw_predicate_test.go "$test_dir/"
cp tests/error_type_predicate_test.go "$test_dir/"
cp tests/function_type_predicate_test.go "$test_dir/"
cp tests/mock_type_predicate_test.go "$test_dir/"
cp tests/mock_throw_behavior_test.go "$test_dir/"
cp tests/location_frames_test.go "$test_dir/"
cp tests/name_encoding_test.go "$test_dir/"
cp tests/json_fields_test.go "$test_dir/"
mkdir "$test_dir/runner"
"$keika_command" emit-go -package main -o "$test_dir/runner/main.go" cmd/vasest/main.km
cp tests/runner_test.go "$test_dir/runner/"
cp tests/runner_timeout_test.go "$test_dir/runner/"
cp tests/runner_junit_test.go "$test_dir/runner/"
cp tests/runner_ambiguous_rerun_test.go "$test_dir/runner/"
cp tests/runner_discovery_test.go "$test_dir/runner/"
cp tests/runner_artifact_results_test.go "$test_dir/runner/"
cp tests/runner_evolving_rerun_test.go "$test_dir/runner/"
cp tests/runner_name_encoding_test.go "$test_dir/runner/"
cp tests/runner_json_fields_test.go "$test_dir/runner/"
cp tests/runner_slice_boundary_test.go "$test_dir/runner/"
cp tests/runner_slice_sequence_test.go "$test_dir/runner/"
cp tests/runner_slice_in_order_test.go "$test_dir/runner/"
cp tests/runner_slice_indexed_predicate_test.go "$test_dir/runner/"
cp tests/runner_slice_elements_test.go "$test_dir/runner/"

cd "$test_dir"
go mod init example.com/vasest-test >/dev/null
go test -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
if [ "${VASEST_RACE:-0}" = "1" ]; then
  go test -race ./...
fi

cd "$project_dir"
"$keika_command" build -o "$test_dir/vasest" cmd/vasest/main.km

library_dir="$test_dir/library"
consumer_dir="$test_dir/consumer"
mkdir "$library_dir"
cp vasest.km go.mod kinmokusei.toml kinmokusei.lock "$library_dir/"
"$keika_command" new app --module example.com/vasest-consumer "$consumer_dir"
mkdir "$consumer_dir/tests" "$consumer_dir/negative"
mkdir "$consumer_dir/timeout"
mkdir "$consumer_dir/timeout_pipes"
cp tests/external_consumer/timeout_pipes/*.test.km "$consumer_dir/timeout_pipes/"
mkdir "$consumer_dir/reporting"
mkdir "$consumer_dir/junit_file_errors"
cp tests/external_consumer/junit_file_errors/*.test.km "$consumer_dir/junit_file_errors/"
mkdir "$consumer_dir/name_encoding"
cp tests/external_consumer/name_encoding/*.test.km "$consumer_dir/name_encoding/"
mkdir "$consumer_dir/json_fields"
cp tests/external_consumer/json_fields/*.test.km "$consumer_dir/json_fields/"
mkdir "$consumer_dir/evolving_rerun"
cp tests/external_consumer/evolving_rerun/*.test.km "$consumer_dir/evolving_rerun/"
mkdir "$consumer_dir/ambiguous_rerun"
cp tests/external_consumer/ambiguous_rerun/*.test.km "$consumer_dir/ambiguous_rerun/"
mkdir "$consumer_dir/regular_discovery" "$consumer_dir/regular_discovery/nested"
cp tests/external_consumer/regular_discovery/*.test.km "$consumer_dir/regular_discovery/"
cp tests/external_consumer/regular_discovery/nested/*.test.km "$consumer_dir/regular_discovery/nested/"
mkdir "$consumer_dir/snapshots"
mkdir "$consumer_dir/mocks"
mkdir "$consumer_dir/cleanup"
mkdir "$consumer_dir/context_table"
mkdir "$consumer_dir/diagnostics"
mkdir "$consumer_dir/fixtures"
mkdir "$consumer_dir/parallel"
mkdir "$consumer_dir/shared_fixture"
mkdir "$consumer_dir/coverage"
cp tests/external_consumer/coverage/*.km "$consumer_dir/coverage/"
mkdir "$consumer_dir/runtime_skip"
cp tests/external_consumer/runtime_skip/*.test.km "$consumer_dir/runtime_skip/"
mkdir "$consumer_dir/fluent"
cp tests/external_consumer/fluent/*.test.km "$consumer_dir/fluent/"
mkdir "$consumer_dir/fluent/type_errors"
cp tests/external_consumer/fluent/type_errors/*.km "$consumer_dir/fluent/type_errors/"
mkdir "$consumer_dir/error_assertions"
cp tests/external_consumer/error_assertions/*.test.km "$consumer_dir/error_assertions/"
mkdir "$consumer_dir/error_matchers" "$consumer_dir/error_matchers/type_errors"
cp tests/external_consumer/error_matchers/*.test.km "$consumer_dir/error_matchers/"
cp tests/external_consumer/error_matchers/type_errors/*.km "$consumer_dir/error_matchers/type_errors/"
mkdir "$consumer_dir/function_matchers" "$consumer_dir/function_matchers/type_errors"
cp tests/external_consumer/function_matchers/*.test.km "$consumer_dir/function_matchers/"
cp tests/external_consumer/function_matchers/type_errors/*.km "$consumer_dir/function_matchers/type_errors/"
mkdir "$consumer_dir/map_matchers" "$consumer_dir/map_matchers/type_errors"
cp tests/external_consumer/map_matchers/*.test.km "$consumer_dir/map_matchers/"
cp tests/external_consumer/map_matchers/type_errors/*.km "$consumer_dir/map_matchers/type_errors/"
mkdir "$consumer_dir/map_predicate_matchers" "$consumer_dir/map_predicate_matchers/type_errors"
cp tests/external_consumer/map_predicate_matchers/*.test.km "$consumer_dir/map_predicate_matchers/"
cp tests/external_consumer/map_predicate_matchers/type_errors/*.km "$consumer_dir/map_predicate_matchers/type_errors/"
mkdir "$consumer_dir/map_collection_predicate" "$consumer_dir/map_collection_predicate/type_errors"
cp tests/external_consumer/map_collection_predicate/*.test.km "$consumer_dir/map_collection_predicate/"
cp tests/external_consumer/map_collection_predicate/type_errors/*.km "$consumer_dir/map_collection_predicate/type_errors/"
mkdir "$consumer_dir/map_entries" "$consumer_dir/map_entries/type_errors"
cp tests/external_consumer/map_entries/*.test.km "$consumer_dir/map_entries/"
cp tests/external_consumer/map_entries/type_errors/*.km "$consumer_dir/map_entries/type_errors/"
mkdir "$consumer_dir/string_boundary" "$consumer_dir/string_boundary/type_errors"
cp tests/external_consumer/string_boundary/*.test.km "$consumer_dir/string_boundary/"
cp tests/external_consumer/string_boundary/type_errors/*.km "$consumer_dir/string_boundary/type_errors/"
mkdir "$consumer_dir/predicate_matchers" "$consumer_dir/predicate_matchers/type_errors"
cp tests/external_consumer/predicate_matchers/*.test.km "$consumer_dir/predicate_matchers/"
cp tests/external_consumer/predicate_matchers/type_errors/*.km "$consumer_dir/predicate_matchers/type_errors/"
mkdir "$consumer_dir/slice_elements" "$consumer_dir/slice_elements/type_errors"
cp tests/external_consumer/slice_elements/*.test.km "$consumer_dir/slice_elements/"
cp tests/external_consumer/slice_elements/type_errors/*.km "$consumer_dir/slice_elements/type_errors/"
mkdir "$consumer_dir/slice_indexed_predicates" "$consumer_dir/slice_indexed_predicates/type_errors"
cp tests/external_consumer/slice_indexed_predicates/*.test.km "$consumer_dir/slice_indexed_predicates/"
cp tests/external_consumer/slice_indexed_predicates/type_errors/*.km "$consumer_dir/slice_indexed_predicates/type_errors/"
mkdir "$consumer_dir/slice_predicate_matchers" "$consumer_dir/slice_predicate_matchers/type_errors"
cp tests/external_consumer/slice_predicate_matchers/*.test.km "$consumer_dir/slice_predicate_matchers/"
cp tests/external_consumer/slice_predicate_matchers/type_errors/*.km "$consumer_dir/slice_predicate_matchers/type_errors/"
mkdir "$consumer_dir/indexed_tables" "$consumer_dir/indexed_tables/type_errors"
mkdir "$consumer_dir/inactive_tables" "$consumer_dir/inactive_tables/type_errors"
cp tests/external_consumer/inactive_tables/*.test.km "$consumer_dir/inactive_tables/"
cp tests/external_consumer/inactive_tables/type_errors/*.km "$consumer_dir/inactive_tables/type_errors/"
mkdir "$consumer_dir/context_hooks" "$consumer_dir/context_hooks/type_errors"
mkdir "$consumer_dir/mock_reset" "$consumer_dir/mock_reset/type_errors"
mkdir "$consumer_dir/mock_throw_behavior" "$consumer_dir/mock_throw_behavior/type_errors"
cp tests/external_consumer/mock_throw_behavior/*.test.km "$consumer_dir/mock_throw_behavior/"
cp tests/external_consumer/mock_throw_behavior/type_errors/*.km "$consumer_dir/mock_throw_behavior/type_errors/"
mkdir "$consumer_dir/mock_type_predicate" "$consumer_dir/mock_type_predicate/type_errors"
cp tests/external_consumer/mock_type_predicate/*.test.km "$consumer_dir/mock_type_predicate/"
cp tests/external_consumer/mock_type_predicate/type_errors/*.km "$consumer_dir/mock_type_predicate/type_errors/"
mkdir "$consumer_dir/function_type_predicate" "$consumer_dir/function_type_predicate/type_errors"
cp tests/external_consumer/function_type_predicate/*.test.km "$consumer_dir/function_type_predicate/"
cp tests/external_consumer/function_type_predicate/type_errors/*.km "$consumer_dir/function_type_predicate/type_errors/"
mkdir "$consumer_dir/error_type_predicate" "$consumer_dir/error_type_predicate/type_errors"
cp tests/external_consumer/error_type_predicate/*.test.km "$consumer_dir/error_type_predicate/"
cp tests/external_consumer/error_type_predicate/type_errors/*.km "$consumer_dir/error_type_predicate/type_errors/"
mkdir "$consumer_dir/function_throw_predicate" "$consumer_dir/function_throw_predicate/type_errors"
cp tests/external_consumer/function_throw_predicate/*.test.km "$consumer_dir/function_throw_predicate/"
cp tests/external_consumer/function_throw_predicate/type_errors/*.km "$consumer_dir/function_throw_predicate/type_errors/"
cp tests/external_consumer/mock_reset/*.test.km "$consumer_dir/mock_reset/"
cp tests/external_consumer/mock_reset/type_errors/*.km "$consumer_dir/mock_reset/type_errors/"
cp tests/external_consumer/context_hooks/*.test.km "$consumer_dir/context_hooks/"
cp tests/external_consumer/context_hooks/type_errors/*.km "$consumer_dir/context_hooks/type_errors/"
mkdir "$consumer_dir/unordered_slice_matchers" "$consumer_dir/unordered_slice_matchers/type_errors"
cp tests/external_consumer/unordered_slice_matchers/*.test.km "$consumer_dir/unordered_slice_matchers/"
cp tests/external_consumer/unordered_slice_matchers/type_errors/*.km "$consumer_dir/unordered_slice_matchers/type_errors/"
mkdir "$consumer_dir/slice_in_order" "$consumer_dir/slice_in_order/type_errors"
cp tests/external_consumer/slice_in_order/*.test.km "$consumer_dir/slice_in_order/"
cp tests/external_consumer/slice_in_order/type_errors/*.km "$consumer_dir/slice_in_order/type_errors/"
mkdir "$consumer_dir/slice_sequence" "$consumer_dir/slice_sequence/type_errors"
cp tests/external_consumer/slice_sequence/*.test.km "$consumer_dir/slice_sequence/"
cp tests/external_consumer/slice_sequence/type_errors/*.km "$consumer_dir/slice_sequence/type_errors/"
mkdir "$consumer_dir/slice_boundary" "$consumer_dir/slice_boundary/type_errors"
cp tests/external_consumer/slice_boundary/*.test.km "$consumer_dir/slice_boundary/"
cp tests/external_consumer/slice_boundary/type_errors/*.km "$consumer_dir/slice_boundary/type_errors/"
mkdir "$consumer_dir/slice_contains_all" "$consumer_dir/slice_contains_all/type_errors"
cp tests/external_consumer/slice_contains_all/*.test.km "$consumer_dir/slice_contains_all/"
cp tests/external_consumer/slice_contains_all/type_errors/*.km "$consumer_dir/slice_contains_all/type_errors/"
mkdir "$consumer_dir/cycle_diagnostics"
cp tests/external_consumer/cycle_diagnostics/*.test.km "$consumer_dir/cycle_diagnostics/"
cp tests/external_consumer/indexed_tables/*.test.km "$consumer_dir/indexed_tables/"
cp tests/external_consumer/indexed_tables/type_errors/*.km "$consumer_dir/indexed_tables/type_errors/"
mkdir "$consumer_dir/wait_value" "$consumer_dir/wait_value/type_errors"
cp tests/external_consumer/wait_value/*.test.km "$consumer_dir/wait_value/"
cp tests/external_consumer/wait_value/type_errors/*.km "$consumer_dir/wait_value/type_errors/"
mkdir "$consumer_dir/float_matchers" "$consumer_dir/float_matchers/type_errors"
cp tests/external_consumer/float_matchers/*.test.km "$consumer_dir/float_matchers/"
cp tests/external_consumer/float_matchers/type_errors/*.km "$consumer_dir/float_matchers/type_errors/"
mkdir "$consumer_dir/ordered_matchers" "$consumer_dir/ordered_matchers/type_errors"
cp tests/external_consumer/ordered_matchers/*.test.km "$consumer_dir/ordered_matchers/"
cp tests/external_consumer/ordered_matchers/type_errors/*.km "$consumer_dir/ordered_matchers/type_errors/"
mkdir "$consumer_dir/mock_matchers" "$consumer_dir/mock_matchers/type_errors"
cp tests/external_consumer/mock_matchers/*.test.km "$consumer_dir/mock_matchers/"
cp tests/external_consumer/mock_matchers/type_errors/*.km "$consumer_dir/mock_matchers/type_errors/"
mkdir "$consumer_dir/mock_predicate_matchers" "$consumer_dir/mock_predicate_matchers/type_errors"
cp tests/external_consumer/mock_predicate_matchers/*.test.km "$consumer_dir/mock_predicate_matchers/"
cp tests/external_consumer/mock_predicate_matchers/type_errors/*.km "$consumer_dir/mock_predicate_matchers/type_errors/"
mkdir "$consumer_dir/mock_return_matchers" "$consumer_dir/mock_return_matchers/type_errors"
cp tests/external_consumer/mock_return_matchers/*.test.km "$consumer_dir/mock_return_matchers/"
cp tests/external_consumer/mock_return_matchers/type_errors/*.km "$consumer_dir/mock_return_matchers/type_errors/"
mkdir "$consumer_dir/mock_return_predicate_matchers" "$consumer_dir/mock_return_predicate_matchers/type_errors"
cp tests/external_consumer/mock_return_predicate_matchers/*.test.km "$consumer_dir/mock_return_predicate_matchers/"
cp tests/external_consumer/mock_return_predicate_matchers/type_errors/*.km "$consumer_dir/mock_return_predicate_matchers/type_errors/"
mkdir "$consumer_dir/mock_thrown_predicate_matchers" "$consumer_dir/mock_thrown_predicate_matchers/type_errors"
cp tests/external_consumer/mock_thrown_predicate_matchers/*.test.km "$consumer_dir/mock_thrown_predicate_matchers/"
cp tests/external_consumer/mock_thrown_predicate_matchers/type_errors/*.km "$consumer_dir/mock_thrown_predicate_matchers/type_errors/"
mkdir "$consumer_dir/mock_outcome_matchers" "$consumer_dir/mock_outcome_matchers/type_errors"
cp tests/external_consumer/mock_outcome_matchers/*.test.km "$consumer_dir/mock_outcome_matchers/"
cp tests/external_consumer/mock_outcome_matchers/type_errors/*.km "$consumer_dir/mock_outcome_matchers/type_errors/"
mkdir "$consumer_dir/mock_error_matchers" "$consumer_dir/mock_error_matchers/type_errors"
cp tests/external_consumer/mock_error_matchers/*.test.km "$consumer_dir/mock_error_matchers/"
cp tests/external_consumer/mock_error_matchers/type_errors/*.km "$consumer_dir/mock_error_matchers/type_errors/"
mkdir "$consumer_dir/repeat"
cp tests/external_consumer/repeat/*.test.km "$consumer_dir/repeat/"
mkdir "$consumer_dir/rerun"
cp tests/external_consumer/rerun/*.test.km "$consumer_dir/rerun/"
mkdir "$consumer_dir/locations"
cp tests/external_consumer/locations/locations.test.km "$consumer_dir/locations/"
cp tests/external_consumer/shared_fixture/shared.test.km "$consumer_dir/shared_fixture/"
cp tests/external_consumer/passing.test.km tests/external_consumer/other.test.km tests/external_consumer/invalid.test.km "$consumer_dir/parallel/"
cp tests/external_consumer/passing.test.km "$consumer_dir/tests/"
cp tests/external_consumer/other.test.km "$consumer_dir/tests/"
cp tests/external_consumer/throws.test.km "$consumer_dir/tests/"
cp tests/external_consumer/tempdir.test.km "$consumer_dir/tests/"
cp tests/external_consumer/table.test.km "$consumer_dir/tests/"
cp tests/external_consumer/statuses.test.km "$consumer_dir/tests/"
cp tests/external_consumer/*.test.km "$consumer_dir/negative/"
cp tests/external_consumer/timeout/slow.test.km "$consumer_dir/timeout/"
cp tests/external_consumer/reporting/unchecked_failure.test.km "$consumer_dir/reporting/"
cp tests/external_consumer/reporting/no_suite.test.km "$consumer_dir/reporting/"
cp tests/external_consumer/reporting/string_diff.test.km "$consumer_dir/reporting/"
cp tests/external_consumer/reporting/xml_escape.test.km "$consumer_dir/reporting/"
cp tests/external_consumer/reporting/waitfor.test.km "$consumer_dir/reporting/"
cp tests/external_consumer/diagnostics/failure_details.test.km "$consumer_dir/diagnostics/"
cp tests/external_consumer/snapshots/snapshot.test.km "$consumer_dir/snapshots/"
cp tests/external_consumer/mocks/mock.test.km "$consumer_dir/mocks/"
cp tests/external_consumer/mocks/action.test.km "$consumer_dir/mocks/"
cp tests/external_consumer/mocks/outcomes.test.km "$consumer_dir/mocks/"
cp tests/external_consumer/cleanup/cleanup.test.km "$consumer_dir/cleanup/"
cp tests/external_consumer/context_table/context_table.test.km "$consumer_dir/context_table/"
cp tests/external_consumer/fixtures/fixtures.test.km "$consumer_dir/fixtures/"
mkdir "$consumer_dir/tests/.hidden" "$consumer_dir/tests/node_modules"
cp tests/external_consumer/invalid.test.km "$consumer_dir/tests/.hidden/"
cp tests/external_consumer/invalid.test.km "$consumer_dir/tests/node_modules/"

cd "$consumer_dir"
"$keika_command" deps add --offline --replace ../library github.com/puffball1567/vasest@v0.1.0
"$keika_command" check tests/passing.test.km
"$keika_command" check tests/throws.test.km
"$keika_command" check tests/tempdir.test.km
"$keika_command" check tests/table.test.km
"$keika_command" check tests/statuses.test.km
"$keika_command" check snapshots/snapshot.test.km
"$keika_command" check reporting/string_diff.test.km
"$keika_command" check reporting/xml_escape.test.km
"$keika_command" check reporting/no_suite.test.km
"$keika_command" check reporting/waitfor.test.km
"$keika_command" check diagnostics/failure_details.test.km
"$keika_command" check mocks/mock.test.km
"$keika_command" check mocks/action.test.km
"$keika_command" check mocks/outcomes.test.km
"$keika_command" check cleanup/cleanup.test.km
"$keika_command" check context_table/context_table.test.km
"$keika_command" check fixtures/fixtures.test.km
"$keika_command" check shared_fixture/shared.test.km

shared_success=$(VASEST_SHARED_RESOURCE=original "$test_dir/vasest" --jobs 2 --shuffle 42 --filter 'success:' shared_fixture)
case "$shared_success" in
  *'3 passed, 0 failed, 1 skipped, 1 todo'*'1 file(s) passed, 0 failed, 0 not run'*) ;;
  *) printf 'unexpected shared fixture selection:\n%s\n' "$shared_success" >&2; exit 1 ;;
esac
for shared_reporter in text json junit; do
  if shared_failure=$("$test_dir/vasest" --reporter "$shared_reporter" shared_fixture 2>&1); then
    printf 'shared fixture failures unexpectedly passed with %s\n' "$shared_reporter" >&2
    exit 1
  fi
  case "$shared_reporter:$shared_failure" in
    text:*'PASS shared values / success: single'*'[beforeAll] (empty error message)'*'[afterAll] shared hook <&> failed'*'[cleanup] (empty error message)'*'[cleanup] first shared cleanup'*'0 file(s) passed, 1 failed'*) ;;
    json:*'"phase":"beforeAll","message":""'*'"phase":"afterAll","message":"shared hook'*'"phase":"cleanup","message":""'*'"phase":"cleanup","message":"first shared cleanup"'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="9" failures="2" skipped="3"'*'[beforeAll] (empty error message)'*'[afterAll] shared hook &lt;&amp;&gt; failed'*'[cleanup] (empty error message)'*'[cleanup] first shared cleanup'*) ;;
    *) printf 'missing shared fixture diagnostics with %s:\n%s\n' "$shared_reporter" "$shared_failure" >&2; exit 1 ;;
  esac
done

for parallel_reporter in text json junit; do
  if parallel_failure=$("$test_dir/vasest" --jobs 2 --reporter "$parallel_reporter" parallel 2>&1); then
    printf 'parallel compile failure unexpectedly passed with %s\n' "$parallel_reporter" >&2
    exit 1
  fi
  case "$parallel_reporter:$parallel_failure" in
    text:*'FILE parallel/invalid.test.km'*'FILE FAILED parallel/invalid.test.km'*'FILE parallel/other.test.km'*'PASS other file / unrelated test'*'FILE parallel/passing.test.km'*'PASS basic assertions / equality'*'2 file(s) passed, 1 failed, 0 not run'*) ;;
    json:*'"type":"file","path":"parallel/invalid.test.km","status":"fail"'*'"type":"file","path":"parallel/other.test.km","status":"pass"'*'"type":"file","path":"parallel/passing.test.km","status":"pass"'*'"passedFiles":2,"failedFiles":1,"notRunFiles":0'*'"jobs":2'*) ;;
    junit:*'<testsuites tests="4" failures="1" skipped="0"'*'<testsuite name="parallel/invalid.test.km"'*'<testsuite name="parallel/other.test.km"'*'<testsuite name="parallel/passing.test.km"'*) ;;
    *) printf 'unexpected parallel output with %s:\n%s\n' "$parallel_reporter" "$parallel_failure" >&2; exit 1 ;;
  esac
done
parallel_selected=$("$test_dir/vasest" --jobs 3 --filter equality --shuffle 42 --shard 2/2 tests)
case "$parallel_selected" in
  *'SHARD 2/2'*'SHUFFLE seed 42'*'PASS basic assertions / equality'*'3 file(s) passed, 0 failed, 0 not run'*) ;;
  *) printf 'unexpected parallel selection:\n%s\n' "$parallel_selected" >&2; exit 1 ;;
esac
if parallel_no_match=$("$test_dir/vasest" --jobs 2 --filter absent --reporter json parallel/passing.test.km 2>&1); then
  printf 'parallel filter without matches unexpectedly passed\n' >&2
  exit 1
fi
case "$parallel_no_match" in
  *'"noMatch":true'*'"jobs":2'*) ;;
  *) printf 'unexpected parallel no-match report:\n%s\n' "$parallel_no_match" >&2; exit 1 ;;
esac

fixture_success=$(VASEST_TYPED_FIXTURE=original "$test_dir/vasest" --filter passes fixtures)
case "$fixture_success" in
  *'PASS typed fixtures / single passes'*'PASS typed fixtures / first passes'*'PASS typed fixtures / last passes'*'3 passed, 0 failed, 3 skipped'*'1 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected typed fixture selection:\n%s\n' "$fixture_success" >&2; exit 1 ;;
esac
for fixture_reporter in text json junit; do
  if fixture_failure=$("$test_dir/vasest" --reporter "$fixture_reporter" fixtures 2>&1); then
    printf 'typed fixture failures unexpectedly passed with %s\n' "$fixture_reporter" >&2
    exit 1
  fi
  case "$fixture_reporter:$fixture_failure" in
    text:*'PASS typed fixtures / single passes'*'[fixture] fixture setup <&> failed'*'FAIL typed fixtures / body fails: fixture body failed'*'[cleanup] fixture cleanup failed'*'PASS typed fixtures / last passes'*'3 passed, 3 failed'*) ;;
    json:*'"phase":"fixture","message":"fixture setup'*'"phase":"body","message":"fixture body failed"'*'"phase":"cleanup","message":"fixture cleanup failed"'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="6" failures="3" skipped="0"'*'[fixture] fixture setup &lt;&amp;&gt; failed'*'[body] fixture body failed'*'[cleanup] fixture cleanup failed'*) ;;
    *) printf 'missing typed fixture diagnostics with %s:\n%s\n' "$fixture_reporter" "$fixture_failure" >&2; exit 1 ;;
  esac
done
if fixture_bail=$("$test_dir/vasest" --bail fixtures 2>&1); then
  printf 'typed fixture failures unexpectedly passed with bail\n' >&2
  exit 1
fi
case "$fixture_bail" in
  *'[fixture] fixture setup <&> failed'*'SKIP typed fixtures / body fails: not run after failure (--bail)'*'2 passed, 1 failed, 3 skipped'*) ;;
  *) printf 'unexpected typed fixture bail:\n%s\n' "$fixture_bail" >&2; exit 1 ;;
esac

for detail_reporter in text json junit; do
  if detail_failure=$("$test_dir/vasest" --reporter "$detail_reporter" diagnostics/failure_details.test.km 2>&1); then
    printf 'diagnostic failures unexpectedly passed with %s\n' "$detail_reporter" >&2
    exit 1
  fi
  case "$detail_reporter:$detail_failure" in
    text:*'[beforeAll] (empty error message)'*'[afterAll] shared cleanup'*'[beforeEach] setup <&> failed'*'[body] (empty error message)'*'[afterEach] afterEach: text; cleanup: still one error'*'[cleanup] (empty error message)'*'[cleanup] first cleanup'*'PASS failure diagnostics / later case passes'*) ;;
    json:*'"phase":"beforeAll","message":""'*'"phase":"afterAll","message":"shared cleanup"'*'"phase":"beforeEach"'*'"phase":"body","message":""'*'"phase":"afterEach"'*'"phase":"cleanup","message":""'*'"phase":"cleanup","message":"first cleanup"'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="6" failures="4" skipped="1"'*'[beforeAll] (empty error message)'*'[afterAll] shared cleanup'*'[beforeEach] setup &lt;&amp;&gt; failed'*'[body] (empty error message)&#xA;[afterEach] afterEach: text; cleanup: still one error&#xA;金木犀&#xA;[cleanup] (empty error message)&#xA;[cleanup] first cleanup'*) ;;
    *) printf 'missing failure details with %s:\n%s\n' "$detail_reporter" "$detail_failure" >&2; exit 1 ;;
  esac
done

context_table_success=$(VASEST_TABLE_FIXTURE=original "$test_dir/vasest" --filter 'first passes' context_table)
case "$context_table_success" in
  *'PASS context matrix / first passes'*'1 passed, 0 failed, 2 skipped'*'1 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected context table selection:\n%s\n' "$context_table_success" >&2; exit 1 ;;
esac
for context_table_reporter in text json junit; do
  if context_table_failure=$("$test_dir/vasest" --reporter "$context_table_reporter" context_table 2>&1); then
    printf 'context table failure unexpectedly passed with %s\n' "$context_table_reporter" >&2
    exit 1
  fi
  case "$context_table_failure" in
    *'expected 3, got 2'*) ;;
    *) printf 'missing context table diagnostic with %s:\n%s\n' "$context_table_reporter" "$context_table_failure" >&2; exit 1 ;;
  esac
  case "$context_table_reporter:$context_table_failure" in
    text:*'PASS context matrix / first passes'*'FAIL context matrix / middle fails'*'PASS context matrix / last still runs'*'2 passed, 1 failed'*) ;;
    json:*'"passed":2,"failed":1'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="3" failures="1" skipped="0"'*) ;;
    *) printf 'unexpected context table summary with %s:\n%s\n' "$context_table_reporter" "$context_table_failure" >&2; exit 1 ;;
  esac
done
if context_table_bail=$("$test_dir/vasest" --bail context_table 2>&1); then
  printf 'context table failure unexpectedly passed with bail\n' >&2
  exit 1
fi
case "$context_table_bail" in
  *'PASS context matrix / first passes'*'FAIL context matrix / middle fails'*'SKIP context matrix / last still runs: not run after failure (--bail)'*'1 passed, 1 failed, 1 skipped'*) ;;
  *) printf 'unexpected context table bail output:\n%s\n' "$context_table_bail" >&2; exit 1 ;;
esac

cleanup_success=$("$test_dir/vasest" --filter 'success:' cleanup)
case "$cleanup_success" in
  *'PASS registered cleanup / success: keeps resources through afterEach'*'PASS registered cleanup / success: cleanup can register more work'*'2 passed, 0 failed, 3 skipped'*'1 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected registered cleanup success:\n%s\n' "$cleanup_success" >&2; exit 1 ;;
esac
for cleanup_reporter in text json junit; do
  if cleanup_failure=$("$test_dir/vasest" --reporter "$cleanup_reporter" cleanup 2>&1); then
    printf 'cleanup failures unexpectedly passed with %s\n' "$cleanup_reporter" >&2
    exit 1
  fi
  case "$cleanup_failure" in
    *'body failure; afterEach: hook failure; cleanup: second cleanup; cleanup: first cleanup'*'cleanup: expected 2, got 1'*) ;;
    *) printf 'missing cleanup diagnostics with %s:\n%s\n' "$cleanup_reporter" "$cleanup_failure" >&2; exit 1 ;;
  esac
  case "$cleanup_reporter:$cleanup_failure" in
    text:*'2 passed, 3 failed'*'0 file(s) passed, 1 failed'*) ;;
    json:*'"passed":2,"failed":3'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="5" failures="3" skipped="0"'*) ;;
    *) printf 'unexpected cleanup summary with %s:\n%s\n' "$cleanup_reporter" "$cleanup_failure" >&2; exit 1 ;;
  esac
done
if cleanup_bail=$("$test_dir/vasest" --bail cleanup 2>&1); then
  printf 'cleanup failure unexpectedly passed with bail\n' >&2
  exit 1
fi
case "$cleanup_bail" in
  *'FAIL registered cleanup / body hook and cleanup failures'*'cleanup: second cleanup; cleanup: first cleanup'*'SKIP registered cleanup / empty cleanup error: not run after failure (--bail)'*'2 passed, 1 failed, 2 skipped'*) ;;
  *) printf 'unexpected registered cleanup bail output:\n%s\n' "$cleanup_bail" >&2; exit 1 ;;
esac

for mock_kind in mock action; do
  mock_fixture="mocks/$mock_kind.test.km"
  mock_suite="mock functions"
  if [ "$mock_kind" = action ]; then
    mock_suite="mock actions"
  fi
  mock_success=$("$test_dir/vasest" --filter 'success:' "$mock_fixture")
  case "$mock_success" in
    *"PASS $mock_suite / success: records typed dependency calls"*"PASS $mock_suite / success: queued implementations fall back to default"*"PASS $mock_suite / success: throwing implementations remain recorded"*'3 passed, 0 failed, 3 skipped'*'1 file(s) passed, 0 failed'*) ;;
    *) printf 'unexpected mock success output:\n%s\n' "$mock_success" >&2; exit 1 ;;
  esac
  for mock_reporter in text json junit; do
    if mock_failure=$("$test_dir/vasest" --reporter "$mock_reporter" "$mock_fixture" 2>&1); then
      printf 'mock assertions unexpectedly passed with %s\n' "$mock_reporter" >&2
      exit 1
    fi
    case "$mock_failure" in
      *'expected mock to be called 2 time(s), got 1'*'expected mock to be called with'*'expected mock call 1 with 2, got 1'*) ;;
      *) printf 'missing mock diagnostics with %s:\n%s\n' "$mock_reporter" "$mock_failure" >&2; exit 1 ;;
    esac
    case "$mock_reporter:$mock_failure" in
      text:*'3 passed, 3 failed'*'0 file(s) passed, 1 failed'*) ;;
      json:*'"passed":3,"failed":3'*'"failedFiles":1'*) ;;
      junit:*'<testsuites tests="6" failures="3" skipped="0"'*) ;;
      *) printf 'unexpected mock summary with %s:\n%s\n' "$mock_reporter" "$mock_failure" >&2; exit 1 ;;
    esac
  done
  if mock_bail=$("$test_dir/vasest" --bail "$mock_fixture" 2>&1); then
    printf 'mock failure unexpectedly passed with bail\n' >&2
    exit 1
  fi
  case "$mock_bail" in
    *"FAIL $mock_suite / call count mismatch"*"SKIP $mock_suite / argument mismatch: not run after failure (--bail)"*"SKIP $mock_suite / nth call mismatch: not run after failure (--bail)"*'3 passed, 1 failed, 2 skipped'*) ;;
    *) printf 'unexpected mock bail output:\n%s\n' "$mock_bail" >&2; exit 1 ;;
  esac
done

outcome_success=$("$test_dir/vasest" --filter 'success:' mocks/outcomes.test.km)
case "$outcome_success" in
  *'PASS mock outcomes / success: records returns and thrown errors'*'PASS mock outcomes / success: preserves typed exceptions'*'PASS mock outcomes / success: records void outcomes'*'3 passed, 0 failed, 3 skipped'*'1 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected mock outcome success:\n%s\n' "$outcome_success" >&2; exit 1 ;;
esac
for outcome_reporter in text json junit; do
  if outcome_failure=$("$test_dir/vasest" --reporter "$outcome_reporter" mocks/outcomes.test.km 2>&1); then
    printf 'mock outcome assertions unexpectedly passed with %s\n' "$outcome_reporter" >&2
    exit 1
  fi
  case "$outcome_failure" in
    *'expected mock call 1 to return 2, got 1'*'expected mock to throw 1 time(s), got 0'*'actual notification failure'*) ;;
    *) printf 'missing mock outcome diagnostics with %s:\n%s\n' "$outcome_reporter" "$outcome_failure" >&2; exit 1 ;;
  esac
  case "$outcome_reporter:$outcome_failure" in
    text:*'3 passed, 3 failed'*'0 file(s) passed, 1 failed'*) ;;
    json:*'"passed":3,"failed":3'*'"failedFiles":1'*) ;;
    junit:*'<testsuites tests="6" failures="3" skipped="0"'*) ;;
    *) printf 'unexpected mock outcome summary with %s:\n%s\n' "$outcome_reporter" "$outcome_failure" >&2; exit 1 ;;
  esac
done
if outcome_bail=$("$test_dir/vasest" --bail mocks/outcomes.test.km 2>&1); then
  printf 'mock outcome failure unexpectedly passed with bail\n' >&2
  exit 1
fi
case "$outcome_bail" in
  *'FAIL mock outcomes / return value mismatch'*'SKIP mock outcomes / throw count mismatch: not run after failure (--bail)'*'SKIP mock outcomes / throw message mismatch: not run after failure (--bail)'*'3 passed, 1 failed, 2 skipped'*) ;;
  *) printf 'unexpected mock outcome bail output:\n%s\n' "$outcome_bail" >&2; exit 1 ;;
esac

list_output=$("$test_dir/vasest" --list)
if [ "$list_output" != "tests/other.test.km
tests/passing.test.km
tests/statuses.test.km
tests/table.test.km
tests/tempdir.test.km
tests/throws.test.km" ]; then
  printf 'unexpected test discovery:\n%s\n' "$list_output" >&2
  exit 1
fi
shard_one=$("$test_dir/vasest" --list --shard 1/2)
shard_two=$("$test_dir/vasest" --list --shard 2/2)
if [ "$shard_one" != "tests/other.test.km
tests/statuses.test.km
tests/tempdir.test.km" ] || [ "$shard_two" != "tests/passing.test.km
tests/table.test.km
tests/throws.test.km" ]; then
  printf 'unexpected shard assignment:\n%s\n%s\n' "$shard_one" "$shard_two" >&2
  exit 1
fi
combined_shards=$(printf '%s\n%s\n' "$shard_one" "$shard_two" | sort)
if [ "$combined_shards" != "$list_output" ]; then
  printf 'shards do not partition the discovered files\n' >&2
  exit 1
fi
if [ "$("$test_dir/vasest" --list --shard 1/1)" != "$list_output" ] || [ -n "$("$test_dir/vasest" --list --shard 7/7)" ]; then
  printf 'single or empty shard listing is incorrect\n' >&2
  exit 1
fi
for invalid_shard in 0/2 3/2 1/0 1 1/2/3 nonsense -1/2; do
  if "$test_dir/vasest" --shard "$invalid_shard" tests >/dev/null 2>&1; then
    printf 'invalid shard %s unexpectedly passed\n' "$invalid_shard" >&2
    exit 1
  fi
done
if "$test_dir/vasest" --shard >/dev/null 2>&1; then
  printf 'missing shard unexpectedly passed\n' >&2
  exit 1
fi

cat > "$test_dir/fast-keika" <<'EOF'
#!/bin/sh
printf '%s\n' '{"success":true,"summary":{"suite":"shuffle probe","passed":1,"failed":0,"skipped":0,"todo":0,"durationNs":0,"failOnTodo":false,"results":[{"name":"probe","passed":true,"status":"pass","message":"","durationNs":0}]}}' >> "$VASEST_REPORT_PATH"
EOF
chmod +x "$test_dir/fast-keika"

cat > "$test_dir/report-keika" <<'EOF'
#!/bin/sh
printf '%s\n' "$VASEST_PROBE_REPORT" >> "$VASEST_REPORT_PATH"
EOF
chmod +x "$test_dir/report-keika"
VASEST_PROBE_REPORT='{ "summary" : { "results": [{"status":"pass","passed":true,"name":"probe","durationNs":0,"message":""}], "todo":0,"skipped":0,"failed":0,"passed":1,"durationNs":0,"failOnTodo":false,"suite":"report probe" }, "success" : true }'
export VASEST_PROBE_REPORT
for probe_reporter in text json junit; do
  KEIKA="$test_dir/report-keika" "$test_dir/vasest" --reporter "$probe_reporter" tests/passing.test.km > "$test_dir/report-probe.out"
done

assert_invalid_report() {
  VASEST_PROBE_REPORT=$1
  for probe_reporter in text json junit; do
    if KEIKA="$test_dir/report-keika" "$test_dir/vasest" --reporter "$probe_reporter" tests/passing.test.km > "$test_dir/report-probe.out" 2>&1; then
      printf 'invalid suite report unexpectedly passed with %s\n' "$probe_reporter" >&2
      exit 1
    fi
    if ! grep -q 'invalid suite report' "$test_dir/report-probe.out"; then
      printf 'missing invalid suite report diagnostic with %s\n' "$probe_reporter" >&2
      cat "$test_dir/report-probe.out" >&2
      exit 1
    fi
    if [ "$probe_reporter" = json ] && grep -q '"type":"suite"' "$test_dir/report-probe.out"; then
      printf 'invalid suite report was forwarded as a JSON suite event\n' >&2
      exit 1
    fi
  done
}
assert_invalid_report 'not JSON'
assert_invalid_report '{"success":true,"summary":null}'
assert_invalid_report '{"success":"true","summary":{"suite":"probe"}}'
assert_invalid_report '{"success":true,"summary":{"suite":" ","passed":1,"results":[{"name":"probe","passed":true,"status":"pass"}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":0,"failed":1,"skipped":0,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":false,"status":"fail","durationNs":0}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":2,"failed":0,"skipped":0,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":true,"status":"pass","durationNs":0}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":0,"failed":0,"skipped":1,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":false,"status":"unknown","durationNs":0}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":1,"failed":0,"skipped":0,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":false,"status":"pass","durationNs":0}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":0,"failed":0,"skipped":1,"todo":0,"durationNs":-1,"results":[]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":0,"failed":0,"skipped":1,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":false,"status":"skip","durationNs":1}]}}'
assert_invalid_report '{"success":false,"summary":{"suite":"probe","passed":0,"failed":1,"results":[{"name":"probe","passed":false,"status":"fail","durationNs":-1}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":0,"failed":0,"skipped":0,"todo":1,"failOnTodo":true,"results":[{"name":"probe","passed":false,"status":"todo"}]}}'
assert_invalid_report '{"success":true,"summary":{"suite":"probe","passed":1,"results":[{"name":"probe","passed":true,"status":"pass","failures":[{"phase":"body","message":"invalid"}]}]}}'
assert_invalid_report '{"success":false,"summary":{"suite":"probe","failed":1,"results":[{"name":"probe","passed":false,"status":"fail","failures":[{"phase":"unknown","message":"invalid"}]}]}}'
assert_invalid_report '{"success":false,"summary":{"suite":"probe","failed":1,"results":[{"name":"probe","passed":false,"status":"fail","failures":[null]}]}}'
VASEST_PROBE_REPORT='{ "summary": {"suite":"probe","passed":0,"failed":1,"skipped":0,"todo":0,"durationNs":0,"results":[{"name":"probe","passed":false,"status":"fail","message":"probe failure","durationNs":0}]}, "success":false }'
for probe_reporter in text json junit; do
  if KEIKA="$test_dir/report-keika" "$test_dir/vasest" --reporter "$probe_reporter" tests/passing.test.km > "$test_dir/report-probe.out" 2>&1; then
    printf 'failed suite report unexpectedly passed with %s\n' "$probe_reporter" >&2
    exit 1
  fi
  if ! grep -Eq 'suite reported failed tests|probe failure' "$test_dir/report-probe.out" || grep -q 'invalid suite report' "$test_dir/report-probe.out"; then
    printf 'valid failed suite report was misclassified with %s\n' "$probe_reporter" >&2
    exit 1
  fi
done
unset VASEST_PROBE_REPORT

shuffle_output=$(KEIKA="$test_dir/fast-keika" "$test_dir/vasest" --shuffle 42 tests)
shuffle_repeat=$(KEIKA="$test_dir/fast-keika" "$test_dir/vasest" --shuffle 42 tests)
shuffled_files=$(printf '%s\n' "$shuffle_output" | awk '$1 == "FILE" { print $2 }')
if [ "$shuffle_output" != "$shuffle_repeat" ] || [ "$shuffled_files" = "$list_output" ]; then
  printf 'shuffle order was not reproducible or did not change\n' >&2
  exit 1
fi
case "$shuffle_output" in
  "SHUFFLE seed 42"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected shuffled output:\n%s\n' "$shuffle_output" >&2; exit 1 ;;
esac
shard_shuffle=$(KEIKA="$test_dir/fast-keika" "$test_dir/vasest" --shard 1/2 --shuffle 42 tests)
shard_shuffle_files=$(printf '%s\n' "$shard_shuffle" | awk '$1 == "FILE" { print $2 }' | sort)
if [ "$shard_shuffle_files" != "$shard_one" ]; then
  printf 'shuffle changed the files assigned to a shard:\n%s\n' "$shard_shuffle" >&2
  exit 1
fi
case "$shard_shuffle" in
  'SHARD 1/2'*'SHUFFLE seed 42'*'3 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected shuffled shard output:\n%s\n' "$shard_shuffle" >&2; exit 1 ;;
esac
empty_shard=$(KEIKA="$test_dir/not-installed-keika" "$test_dir/vasest" --shard 7/7 --filter absent tests)
case "$empty_shard" in
  'SHARD 7/7'*'0 file(s) passed, 0 failed, 0 not run') ;;
  *) printf 'unexpected empty shard output:\n%s\n' "$empty_shard" >&2; exit 1 ;;
esac
empty_shard_json=$("$test_dir/vasest" --shard 7/7 --reporter json tests)
case "$empty_shard_json" in
  '{"type":"summary","passedFiles":0,"failedFiles":0,"notRunFiles":0,"noMatch":false'*'"shard":"7/7","jobs":1,'*'"excludedFiles":0}') ;;
  *) printf 'unexpected empty JSON shard:\n%s\n' "$empty_shard_json" >&2; exit 1 ;;
esac
empty_shard_junit=$("$test_dir/vasest" --shard 7/7 --reporter junit tests)
case "$empty_shard_junit" in
  '<?xml version="1.0" encoding="UTF-8"?>'*'<testsuites tests="0" failures="0" skipped="0"'*'</testsuites>') ;;
  *) printf 'unexpected empty JUnit shard:\n%s\n' "$empty_shard_junit" >&2; exit 1 ;;
esac
for invalid_seed in -1 nonsense; do
  if "$test_dir/vasest" --shuffle "$invalid_seed" tests >/dev/null 2>&1; then
    printf 'invalid shuffle seed %s unexpectedly passed\n' "$invalid_seed" >&2
    exit 1
  fi
done
if "$test_dir/vasest" --shuffle >/dev/null 2>&1; then
  printf 'missing shuffle seed unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --shuffle 42 >/dev/null 2>&1; then
  printf 'combined list and shuffle unexpectedly passed\n' >&2
  exit 1
fi

pass_output=$("$test_dir/vasest")
case "$pass_output" in
  *"SKIP status cases / disabled"*"TODO status cases / planned"*"PASS status cases / regex and absence"*"PASS absolute value matrix / negative"*"PASS absolute value matrix / zero"*"PASS absolute value matrix / positive"*"PASS temporary directory fixture / cleans up after success"*"PASS temporary directory fixture / cleans up after thrown error"*"PASS temporary directory fixture / restores environment after nested scopes"*"PASS temporary directory fixture / restores environment after thrown error"*"PASS throw assertions / captures thrown error"*"PASS throw assertions / checks message"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected passing test output:\n%s\n' "$pass_output" >&2; exit 1 ;;
esac
shard_pass=$("$test_dir/vasest" --shard 2/2 tests)
case "$shard_pass" in
  'SHARD 2/2'*'FILE tests/passing.test.km'*'FILE tests/table.test.km'*'FILE tests/throws.test.km'*'3 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected real shard output:\n%s\n' "$shard_pass" >&2; exit 1 ;;
esac

json_output=$("$test_dir/vasest" --reporter json --shard 1/1 tests/passing.test.km)
case "$json_output" in
  *'"type":"suite"'*'"success":true'*'"suite":"basic assertions"'*'"durationNs":'*'"type":"file"'*'"status":"pass"'*'"durationNs":'*'"type":"summary"'*'"passedFiles":1'*'"failedFiles":0'*'"shard":"1/1"'*) ;;
  *) printf 'unexpected JSON reporter output:\n%s\n' "$json_output" >&2; exit 1 ;;
esac
junit_output=$("$test_dir/vasest" --reporter junit tests/statuses.test.km)
case "$junit_output" in
  '<?xml version="1.0" encoding="UTF-8"?>'*'<testsuites tests="3" failures="0" skipped="2"'*'<testsuite name="tests/statuses.test.km" tests="3" failures="0" skipped="2"'*'<testcase classname="status cases" name="disabled"'*'<skipped message="skip"/>'*'<testcase classname="status cases" name="planned"'*'<skipped message="todo"/>'*'<testcase classname="status cases" name="regex and absence"'*'<system-out>'*'</testsuites>') ;;
  *) printf 'unexpected JUnit reporter output:\n%s\n' "$junit_output" >&2; exit 1 ;;
esac
if junit_todo=$("$test_dir/vasest" --reporter junit --fail-on-todo tests/statuses.test.km 2>&1); then
  printf 'JUnit todo policy unexpectedly passed\n' >&2
  exit 1
fi
case "$junit_todo" in
  *'<testsuites tests="3" failures="1" skipped="1"'*'<testcase classname="status cases" name="planned"'*'<failure message="todo tests are not allowed (--fail-on-todo)"'*) ;;
  *) printf 'unexpected JUnit todo policy output:\n%s\n' "$junit_todo" >&2; exit 1 ;;
esac
if unchecked_output=$("$test_dir/vasest" reporting/unchecked_failure.test.km 2>&1); then
  printf 'unchecked failing suite unexpectedly passed\n' >&2
  exit 1
fi
case "$unchecked_output" in
  *"FAIL unchecked suite / fails"*"FILE FAILED reporting/unchecked_failure.test.km suite reported failed tests"*"0 file(s) passed, 1 failed"*) ;;
  *) printf 'unexpected unchecked suite output:\n%s\n' "$unchecked_output" >&2; exit 1 ;;
esac
if unchecked_json=$("$test_dir/vasest" --reporter json reporting/unchecked_failure.test.km 2>&1); then
  printf 'unchecked failing suite unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$unchecked_json" in
  *'"success":false'*'"type":"file"'*'"status":"fail"'*'"error":"suite reported failed tests"'*'"type":"summary"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected unchecked JSON output:\n%s\n' "$unchecked_json" >&2; exit 1 ;;
esac
if no_suite=$("$test_dir/vasest" reporting/no_suite.test.km 2>&1); then
  printf 'unrun suite unexpectedly passed\n' >&2
  exit 1
fi
case "$no_suite" in
  *'main returned without running the suite'*'FILE FAILED reporting/no_suite.test.km no test suites ran'*'0 file(s) passed, 1 failed'*) ;;
  *) printf 'unexpected unrun suite output:\n%s\n' "$no_suite" >&2; exit 1 ;;
esac
if no_suite_json=$("$test_dir/vasest" --reporter json reporting/no_suite.test.km 2>&1); then
  printf 'unrun suite unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$no_suite_json" in
  *'"type":"file"'*'"status":"fail"'*'"output":"main returned without running the suite\n"'*'"error":"no test suites ran"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON unrun suite output:\n%s\n' "$no_suite_json" >&2; exit 1 ;;
esac
if no_suite_junit=$("$test_dir/vasest" --reporter junit reporting/no_suite.test.km 2>&1); then
  printf 'unrun suite unexpectedly passed with JUnit reporter\n' >&2
  exit 1
fi
case "$no_suite_junit" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<testcase classname="reporting/no_suite.test.km" name="&lt;file&gt;"'*'<failure message="no test suites ran"'*'<system-out>main returned without running the suite'*) ;;
  *) printf 'unexpected JUnit unrun suite output:\n%s\n' "$no_suite_junit" >&2; exit 1 ;;
esac
if no_suite_bail=$("$test_dir/vasest" --bail reporting 2>&1); then
  printf 'unrun suite unexpectedly passed with bail\n' >&2
  exit 1
fi
case "$no_suite_bail" in
  *'FILE reporting/no_suite.test.km'*'FILE FAILED reporting/no_suite.test.km no test suites ran'*'0 file(s) passed, 1 failed, 4 not run'*) ;;
  *) printf 'unexpected bail result for unrun suite:\n%s\n' "$no_suite_bail" >&2; exit 1 ;;
esac
waitfor_pass=$("$test_dir/vasest" --filter retries reporting/waitfor.test.km)
case "$waitfor_pass" in
  *'PASS waitFor cases / retries an assertion'*'1 file(s) passed, 0 failed'*) ;;
  *) printf 'unexpected waitFor passing output:\n%s\n' "$waitfor_pass" >&2; exit 1 ;;
esac
if waitfor_failure=$("$test_dir/vasest" reporting/waitfor.test.km 2>&1); then
  printf 'waitFor timeout unexpectedly passed\n' >&2
  exit 1
fi
case "$waitfor_failure" in
  *'PASS waitFor cases / retries an assertion'*'FAIL waitFor cases / times out with the last failure: waitFor timed out after 5ms'*'still pending'*'0 file(s) passed, 1 failed'*) ;;
  *) printf 'unexpected waitFor timeout output:\n%s\n' "$waitfor_failure" >&2; exit 1 ;;
esac
if waitfor_json=$("$test_dir/vasest" --reporter json --filter 'times out' reporting/waitfor.test.km 2>&1); then
  printf 'waitFor timeout unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$waitfor_json" in
  *'waitFor timed out after 5ms'*'still pending'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON waitFor timeout:\n%s\n' "$waitfor_json" >&2; exit 1 ;;
esac
if string_diff=$("$test_dir/vasest" reporting/string_diff.test.km 2>&1); then
  printf 'different strings unexpectedly passed\n' >&2
  exit 1
fi
case "$string_diff" in
  *'strings differ: line 2: expected "金木犀", got "銀木犀"'*'0 file(s) passed, 1 failed'*) ;;
  *) printf 'unexpected string difference output:\n%s\n' "$string_diff" >&2; exit 1 ;;
esac
if string_diff_json=$("$test_dir/vasest" --reporter json reporting/string_diff.test.km 2>&1); then
  printf 'different strings unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$string_diff_json" in
  *'strings differ: line 2: expected \"金木犀\", got \"銀木犀\"'*'"status":"fail"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON string difference:\n%s\n' "$string_diff_json" >&2; exit 1 ;;
esac
if string_diff_junit=$("$test_dir/vasest" --reporter junit reporting/string_diff.test.km 2>&1); then
  printf 'different strings unexpectedly passed with JUnit reporter\n' >&2
  exit 1
fi
case "$string_diff_junit" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<failure message="strings differ: line 2: expected &quot;金木犀&quot;, got &quot;銀木犀&quot;"'*) ;;
  *) printf 'unexpected JUnit string difference:\n%s\n' "$string_diff_junit" >&2; exit 1 ;;
esac
if junit_escape=$("$test_dir/vasest" --reporter junit reporting/xml_escape.test.km 2>&1); then
  printf 'JUnit escaping fixture unexpectedly passed\n' >&2
  exit 1
fi
case "$junit_escape" in
  *'<testcase classname="xml &amp; &lt;suite&gt;" name="value &quot;quoted&quot;"'*'<failure message="bad &lt;&amp;&gt; &quot;quoted&quot;"'*'<system-out>output &lt;&amp;&gt; &quot;quoted&quot;'*) ;;
  *) printf 'unexpected JUnit escaping output:\n%s\n' "$junit_escape" >&2; exit 1 ;;
esac
if command -v python3 >/dev/null 2>&1; then
  printf '%s' "$junit_escape" | python3 -c 'import sys, xml.etree.ElementTree as ET; root = ET.parse(sys.stdin).getroot(); case = root.find("./testsuite/testcase"); assert root.attrib["failures"] == "1"; assert case.attrib["classname"] == "xml & <suite>"; assert case.attrib["name"] == "value \"quoted\""; assert case.find("failure").attrib["message"] == "bad <&> \"quoted\""'
fi
if json_invalid_output=$("$test_dir/vasest" --reporter json negative/invalid.test.km 2>&1); then
  printf 'invalid Kinmokusei source unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$json_invalid_output" in
  *'"type":"file"'*'"status":"fail"'*'"type":"summary"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON compilation output:\n%s\n' "$json_invalid_output" >&2; exit 1 ;;
esac
if junit_invalid=$("$test_dir/vasest" --reporter junit negative/invalid.test.km 2>&1); then
  printf 'invalid Kinmokusei source unexpectedly passed with JUnit reporter\n' >&2
  exit 1
fi
case "$junit_invalid" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<testcase classname="negative/invalid.test.km" name="&lt;file&gt;"'*'<failure message="exit status 1"'*'<system-out>'*) ;;
  *) printf 'unexpected JUnit compilation output:\n%s\n' "$junit_invalid" >&2; exit 1 ;;
esac
if json_no_match=$("$test_dir/vasest" --reporter json --filter absent tests/passing.test.km 2>&1); then
  printf 'unmatched JSON filter unexpectedly passed\n' >&2
  exit 1
fi
case "$json_no_match" in
  *'"type":"summary"'*'"noMatch":true'*) ;;
  *) printf 'unexpected JSON no-match output:\n%s\n' "$json_no_match" >&2; exit 1 ;;
esac
if junit_no_match=$("$test_dir/vasest" --reporter junit --filter absent tests/passing.test.km 2>&1); then
  printf 'unmatched JUnit filter unexpectedly passed\n' >&2
  exit 1
fi
case "$junit_no_match" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<testsuite name="&lt;filter&gt;"'*'<failure message="no tests match &quot;absent&quot;"'*) ;;
  *) printf 'unexpected JUnit no-match output:\n%s\n' "$junit_no_match" >&2; exit 1 ;;
esac
if "$test_dir/vasest" --reporter xml >/dev/null 2>&1; then
  printf 'unknown reporter unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --reporter json >/dev/null 2>&1; then
  printf 'combined list and JSON reporter unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --reporter junit >/dev/null 2>&1; then
  printf 'combined list and JUnit reporter unexpectedly passed\n' >&2
  exit 1
fi

if missing_snapshot=$("$test_dir/vasest" snapshots/snapshot.test.km 2>&1); then
  printf 'missing snapshots unexpectedly passed\n' >&2
  exit 1
fi
case "$missing_snapshot" in
  *'snapshot "greeting / 金木犀" is missing'*"0 file(s) passed, 1 failed"*) ;;
  *) printf 'unexpected missing snapshot output:\n%s\n' "$missing_snapshot" >&2; exit 1 ;;
esac
snapshot_update=$("$test_dir/vasest" --update-snapshots snapshots/snapshot.test.km)
case "$snapshot_update" in
  *"PASS snapshot cases / records text"*"PASS snapshot cases / records multiline text"*"PASS snapshot cases / records structured JSON"*"PASS snapshot cases / first use of a shared name"*"PASS snapshot cases / second use of a shared name"*"1 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected snapshot update output:\n%s\n' "$snapshot_update" >&2; exit 1 ;;
esac
snapshot_count=$(find snapshots/__snapshots__/snapshot.test.km -name '*.snap' -type f | wc -l)
if [ "$snapshot_count" -ne 5 ]; then
  printf 'expected five snapshot files, found %s\n' "$snapshot_count" >&2
  exit 1
fi
"$test_dir/vasest" snapshots/snapshot.test.km >/dev/null
if changed_snapshot=$(VASEST_SNAPSHOT_VARIANT=changed "$test_dir/vasest" snapshots/snapshot.test.km 2>&1); then
  printf 'changed snapshot unexpectedly passed without update\n' >&2
  exit 1
fi
case "$changed_snapshot" in
  *'snapshot "greeting / 金木犀" mismatch: line 2: expected "stable", got "changed"'*"0 file(s) passed, 1 failed"*) ;;
  *) printf 'unexpected snapshot mismatch output:\n%s\n' "$changed_snapshot" >&2; exit 1 ;;
esac
if changed_snapshot_json=$(VASEST_SNAPSHOT_VARIANT=changed "$test_dir/vasest" --reporter json snapshots/snapshot.test.km 2>&1); then
  printf 'changed snapshot unexpectedly passed with JSON reporter\n' >&2
  exit 1
fi
case "$changed_snapshot_json" in
  *'"success":false'*'"status":"fail"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON snapshot mismatch:\n%s\n' "$changed_snapshot_json" >&2; exit 1 ;;
esac
VASEST_SNAPSHOT_VARIANT=changed "$test_dir/vasest" --update-snapshots snapshots/snapshot.test.km >/dev/null
VASEST_SNAPSHOT_VARIANT=changed "$test_dir/vasest" snapshots/snapshot.test.km >/dev/null
if changed_structured=$(VASEST_SNAPSHOT_VARIANT=changed VASEST_JSON_VARIANT=changed "$test_dir/vasest" snapshots/snapshot.test.km 2>&1); then
  printf 'changed structured snapshot unexpectedly passed without update\n' >&2
  exit 1
fi
case "$changed_structured" in
  *'snapshot "structured" mismatch: line 2: expected "  \"title\": \"stable\",", got "  \"title\": \"changed\","'*'0 file(s) passed, 1 failed'*) ;;
  *) printf 'unexpected structured snapshot mismatch:\n%s\n' "$changed_structured" >&2; exit 1 ;;
esac
VASEST_SNAPSHOT_VARIANT=changed VASEST_JSON_VARIANT=changed "$test_dir/vasest" --update-snapshots snapshots/snapshot.test.km >/dev/null
VASEST_SNAPSHOT_VARIANT=changed VASEST_JSON_VARIANT=changed "$test_dir/vasest" snapshots/snapshot.test.km >/dev/null
if "$test_dir/vasest" --list --update-snapshots >/dev/null 2>&1; then
  printf 'combined list and snapshot update unexpectedly passed\n' >&2
  exit 1
fi

if failure_output=$("$test_dir/vasest" negative 2>&1); then
  printf 'failing and invalid tests unexpectedly passed\n' >&2
  exit 1
fi
case "$failure_output" in
  *"FAIL failure handling / first failure"*"PASS failure handling / later test still runs"*"invalid.test.km"*"PASS basic assertions / equality"*"FAIL assertion failures / invalid regex"*"FAIL assertion failures / unexpected substring"*"FAIL assertion failures / approximate mismatch"*"FAIL assertion failures / explicit failure: intentional failure"*"PASS assertion failures / later case still runs"*"PASS matrix failures / first passes"*"FAIL matrix failures / middle fails"*"PASS matrix failures / last still runs"*"FAIL throw failures / missing exception"*"FAIL throw failures / message mismatch"*"PASS throw failures / continues after failure"*"6 file(s) passed, 5 failed"*) ;;
  *) printf 'unexpected failure output:\n%s\n' "$failure_output" >&2; exit 1 ;;
esac

if bail_output=$("$test_dir/vasest" --bail negative 2>&1); then
  printf 'bail run unexpectedly passed\n' >&2
  exit 1
fi
case "$bail_output" in
  *"FAIL failure handling / first failure"*"SKIP failure handling / later test still runs: not run after failure (--bail)"*"0 file(s) passed, 1 failed, 10 not run"*) ;;
  *) printf 'unexpected bail output:\n%s\n' "$bail_output" >&2; exit 1 ;;
esac
case "$bail_output" in
  *"FILE negative/invalid.test.km"*) printf 'bail did not stop later files\n' >&2; exit 1 ;;
esac

bail_pass_output=$("$test_dir/vasest" --bail)
case "$bail_pass_output" in
  *"6 file(s) passed, 0 failed, 0 not run"*) ;;
  *) printf 'unexpected successful bail output:\n%s\n' "$bail_pass_output" >&2; exit 1 ;;
esac

if "$test_dir/vasest" missing >/dev/null 2>&1; then
  printf 'missing test path unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" main.km >/dev/null 2>&1; then
  printf 'non-test file unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --unknown >/dev/null 2>&1; then
  printf 'unknown option unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" tests negative >/dev/null 2>&1; then
  printf 'multiple input paths unexpectedly passed\n' >&2
  exit 1
fi
mkdir empty
if "$test_dir/vasest" empty >/dev/null 2>&1; then
  printf 'empty test directory unexpectedly passed\n' >&2
  exit 1
fi
if junit_empty=$("$test_dir/vasest" --reporter junit empty 2>&1); then
  printf 'empty test directory unexpectedly passed with JUnit reporter\n' >&2
  exit 1
fi
case "$junit_empty" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<testsuite name="empty"'*'<failure message="no *.test.km files found"'*) ;;
  *) printf 'unexpected empty JUnit report:\n%s\n' "$junit_empty" >&2; exit 1 ;;
esac

timeout_pass_output=$("$test_dir/vasest" --timeout 30s --filter equality tests/passing.test.km)
case "$timeout_pass_output" in
  *"PASS basic assertions / equality"*"1 passed, 0 failed, 1 skipped"*"1 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected passing timeout output:\n%s\n' "$timeout_pass_output" >&2; exit 1 ;;
esac
if timeout_compile_output=$("$test_dir/vasest" --timeout 30s negative/invalid.test.km 2>&1); then
  printf 'invalid Kinmokusei source unexpectedly built with timeout\n' >&2
  exit 1
fi
case "$timeout_compile_output" in
  *"FILE FAILED negative/invalid.test.km"*"0 file(s) passed, 1 failed"*) ;;
  *) printf 'unexpected timeout compilation output:\n%s\n' "$timeout_compile_output" >&2; exit 1 ;;
esac

printf '#!/bin/sh\nexec sleep 5\n' > "$test_dir/slow-executable"
chmod +x "$test_dir/slow-executable"
printf '#!/bin/sh\nif [ "$1" = build ]; then\n  cp "$VASEST_SLOW_EXECUTABLE" "$3"\n  exit 0\nfi\nexit 2\n' > "$test_dir/slow-keika"
chmod +x "$test_dir/slow-keika"
if timeout_output=$(VASEST_SLOW_EXECUTABLE="$test_dir/slow-executable" KEIKA="$test_dir/slow-keika" "$test_dir/vasest" --timeout 100ms tests 2>&1); then
  printf 'timed-out tests unexpectedly passed\n' >&2
  exit 1
fi
case "$timeout_output" in
  *"FILE FAILED tests/other.test.km timed out after 100ms"*"0 file(s) passed, 6 failed, 0 not run"*) ;;
  *) printf 'unexpected timeout output:\n%s\n' "$timeout_output" >&2; exit 1 ;;
esac
if timeout_bail_output=$(VASEST_SLOW_EXECUTABLE="$test_dir/slow-executable" KEIKA="$test_dir/slow-keika" "$test_dir/vasest" --bail --timeout 100ms tests 2>&1); then
  printf 'timed-out bail run unexpectedly passed\n' >&2
  exit 1
fi
case "$timeout_bail_output" in
  *"FILE FAILED tests/other.test.km timed out after 100ms"*"0 file(s) passed, 1 failed, 5 not run"*) ;;
  *) printf 'unexpected timeout bail output:\n%s\n' "$timeout_bail_output" >&2; exit 1 ;;
esac
if json_timeout_output=$(VASEST_SLOW_EXECUTABLE="$test_dir/slow-executable" KEIKA="$test_dir/slow-keika" "$test_dir/vasest" --reporter json --timeout 100ms tests/other.test.km 2>&1); then
  printf 'timed-out JSON run unexpectedly passed\n' >&2
  exit 1
fi
case "$json_timeout_output" in
  *'"type":"file"'*'"status":"fail"'*'"error":"timed out after 100ms"'*'"type":"summary"'*'"failedFiles":1'*) ;;
  *) printf 'unexpected JSON timeout output:\n%s\n' "$json_timeout_output" >&2; exit 1 ;;
esac
if junit_timeout_output=$(VASEST_SLOW_EXECUTABLE="$test_dir/slow-executable" KEIKA="$test_dir/slow-keika" "$test_dir/vasest" --reporter junit --timeout 100ms tests/other.test.km 2>&1); then
  printf 'timed-out JUnit run unexpectedly passed\n' >&2
  exit 1
fi
case "$junit_timeout_output" in
  *'<testsuites tests="1" failures="1" skipped="0"'*'<testcase classname="tests/other.test.km" name="&lt;file&gt;"'*'<failure message="timed out after 100ms"'*) ;;
  *) printf 'unexpected JUnit timeout output:\n%s\n' "$junit_timeout_output" >&2; exit 1 ;;
esac
if real_timeout_output=$(VASEST_TIMEOUT_STARTED="$test_dir/timeout-started" VASEST_TIMEOUT_MARKER="$test_dir/timeout-marker" "$test_dir/vasest" --timeout 500ms timeout/slow.test.km 2>&1); then
  printf 'slow Kinmokusei test unexpectedly passed\n' >&2
  exit 1
fi
case "$real_timeout_output" in
  *"FILE FAILED timeout/slow.test.km timed out after 500ms"*) ;;
  *) printf 'unexpected real timeout output:\n%s\n' "$real_timeout_output" >&2; exit 1 ;;
esac
if [ ! -e "$test_dir/timeout-started" ]; then
  printf 'slow Kinmokusei test did not reach its body\n' >&2
  exit 1
fi
sleep 3
if [ -e "$test_dir/timeout-marker" ]; then
  printf 'Kinmokusei test continued after its timeout\n' >&2
  exit 1
fi
for invalid_timeout in 0 -1s nonsense; do
  if "$test_dir/vasest" --timeout "$invalid_timeout" tests >/dev/null 2>&1; then
    printf 'invalid timeout %s unexpectedly passed\n' "$invalid_timeout" >&2
    exit 1
  fi
done
if "$test_dir/vasest" --timeout >/dev/null 2>&1; then
  printf 'missing timeout value unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --timeout 1s >/dev/null 2>&1; then
  printf 'combined list and timeout unexpectedly passed\n' >&2
  exit 1
fi

filter_output=$("$test_dir/vasest" --filter equality)
case "$filter_output" in
  *"SKIP other file: no tests match"*"PASS basic assertions / equality"*"1 passed, 0 failed, 1 skipped"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected filtered test output:\n%s\n' "$filter_output" >&2; exit 1 ;;
esac
zero_output=$("$test_dir/vasest" --filter zero)
case "$zero_output" in
  *"PASS absolute value matrix / zero"*"1 passed, 0 failed, 2 skipped"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected parameterized filter output:\n%s\n' "$zero_output" >&2; exit 1 ;;
esac
bail_filter_output=$("$test_dir/vasest" --bail --filter zero)
case "$bail_filter_output" in
  *"PASS absolute value matrix / zero"*"6 file(s) passed, 0 failed, 0 not run"*) ;;
  *) printf 'unexpected combined bail/filter output:\n%s\n' "$bail_filter_output" >&2; exit 1 ;;
esac
disabled_output=$("$test_dir/vasest" --filter disabled)
case "$disabled_output" in
  *"SKIP status cases / disabled"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected filtered skip output:\n%s\n' "$disabled_output" >&2; exit 1 ;;
esac
planned_output=$("$test_dir/vasest" --filter planned)
case "$planned_output" in
  *"TODO status cases / planned"*"6 file(s) passed, 0 failed"*) ;;
  *) printf 'unexpected filtered todo output:\n%s\n' "$planned_output" >&2; exit 1 ;;
esac
if strict_todo_output=$("$test_dir/vasest" --fail-on-todo tests/statuses.test.km 2>&1); then
  printf 'todo case unexpectedly passed in strict mode\n' >&2
  exit 1
fi
case "$strict_todo_output" in
  *"TODO status cases / planned"*"FAIL status cases: todo tests are not allowed (--fail-on-todo)"*"0 file(s) passed, 1 failed"*) ;;
  *) printf 'unexpected strict todo output:\n%s\n' "$strict_todo_output" >&2; exit 1 ;;
esac
if strict_todo_json=$("$test_dir/vasest" --reporter json --fail-on-todo tests/statuses.test.km 2>&1); then
  printf 'todo case unexpectedly passed in strict JSON mode\n' >&2
  exit 1
fi
case "$strict_todo_json" in
  *'"success":false'*'"todo":1'*'"type":"file"'*'"status":"fail"'*'"type":"summary"'*'"failOnTodo":true'*) ;;
  *) printf 'unexpected strict todo JSON output:\n%s\n' "$strict_todo_json" >&2; exit 1 ;;
esac
"$test_dir/vasest" --fail-on-todo --filter disabled tests/statuses.test.km >/dev/null
if "$test_dir/vasest" --list --fail-on-todo >/dev/null 2>&1; then
  printf 'combined list and strict todo unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --filter absent >/dev/null 2>&1; then
  printf 'filter with no matches unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --bail --filter absent >/dev/null 2>&1; then
  printf 'bail filter with no matches unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --filter >/dev/null 2>&1; then
  printf 'missing filter value unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --filter equality >/dev/null 2>&1; then
  printf 'combined list and filter unexpectedly passed\n' >&2
  exit 1
fi
if "$test_dir/vasest" --list --bail >/dev/null 2>&1; then
  printf 'combined list and bail unexpectedly passed\n' >&2
  exit 1
fi

cd "$test_dir"
VASEST_INTEGRATION_RUNNER="$test_dir/vasest" VASEST_INTEGRATION_CONSUMER="$consumer_dir" KEIKA="$keika_command" go test -v -timeout 20m -count=1 -run '^Test(Watch|Coverage|FailureLocations|Artifacts|ArtifactResults|Rerun|Configuration|ConfigurationWatch|FilePatterns|FilePatternsWatch|Repeat|TimeoutPipes|JUnitFileErrors|UnreadableReports|AmbiguousRerun|EvolvingRerun|NameEncoding|JSONFields|RegularDiscovery|ErrorAssertions|ErrorMatchers|FunctionMatchers|MapMatchers|MapPredicateMatchers|MapCollectionPredicate|MapEntries|StringBoundary|SliceBoundary|SliceSequence|SliceInOrder|PredicateMatchers|SlicePredicateMatchers|SliceIndexedPredicates|SliceElements|UnorderedSliceMatchers|SliceContainsAll|CycleDiagnostics|OrderedMatchers|FloatMatchers|WaitValue|IndexedTables|InactiveTables|ContextHooks|MockReset|ErrorTypePredicate|MockThrowBehavior|MockTypePredicate|FunctionTypePredicate|FunctionThrowPredicate|MockMatchers|MockPredicateMatchers|MockReturnMatchers|MockReturnPredicateMatchers|MockThrownPredicateMatchers|MockOutcomeMatchers|MockErrorMatchers|RuntimeSkip|FluentMatchers)RealKinmokusei$' ./runner
