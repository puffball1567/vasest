package vasest

// A real Go line directive verifies that compiler-provided mappings can flow
// through runtime.Caller without inventing a Kinmokusei source position.
//
//line mapped/source.test.km:40
func mappedAssertionFailure() {
	expectTrue(false)
}
