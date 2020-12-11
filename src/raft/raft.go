package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"
)
import "labrpc"

// import "bytes"
// import "encoding/gob"

const DebugCM = 1

func (rf *Raft) dlog(format string, args ...interface{}) {
	if DebugCM > 0 {
		format = fmt.Sprintf("[%d] ", rf.me) + format
		log.Printf(format, args...)
	}
}

type LogEntry struct {
	Command interface{}
	Term    int
}

type CMState int

const (
	Follower CMState = iota
	Candidate
	Leader
	Dead
)

func (s CMState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	case Dead:
		return "Dead"
	default:
		panic("unreachable")
	}
}

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make().
//
type ApplyMsg struct {
	Index       int
	Command     interface{}
	UseSnapshot bool   // ignore for lab2; only used in lab3
	Snapshot    []byte // ignore for lab2; only used in lab3
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex
	peers     []*labrpc.ClientEnd
	persister *Persister
	me        int // index into peers[]

	// Persistent Raft state on all servers
	currentTerm int
	votedFor    int
	log         []LogEntry

	// Volatile Raft state on all servers
	state              CMState
	electionResetEvent time.Time
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	return rf.currentTerm, rf.state == Leader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here.
	// Example:
	// w := new(bytes.Buffer)
	// e := gob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	// Your code here.
	// Example:
	// r := bytes.NewBuffer(data)
	// d := gob.NewDecoder(r)
	// d.Decode(&rf.xxx)
	// d.Decode(&rf.yyy)
}

//
// example RequestVote RPC arguments structure.
//
type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
//
type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state == Dead {
		return
	}
	rf.dlog("RequestVote: %+v [currentTerm=%d, votedFor=%d]",
		args, rf.currentTerm, rf.votedFor)

	if args.Term > rf.currentTerm {
		rf.dlog("... term out of date in RequestVote")
		rf.becomeFollower(args.Term)
	}

	if rf.currentTerm == args.Term &&
		(rf.votedFor == -1 || rf.votedFor == args.CandidateId) {
		reply.VoteGranted = true
		rf.votedFor = args.CandidateId
		rf.electionResetEvent = time.Now()
	} else {
		reply.VoteGranted = false
	}
	reply.Term = rf.currentTerm
	rf.dlog("... RequestVote reply: %+v", reply)
	return
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// returns true if labrpc says the RPC was delivered.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

type AppendEntriesArgs struct {
	Term     int
	LeaderId int

	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

func (rf *Raft) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state == Dead {
		return
	}
	rf.dlog("AppendEntries: %+v", args)

	if args.Term > rf.currentTerm {
		rf.dlog("... term out of date in AppendEntries")
		rf.becomeFollower(args.Term)
	}

	reply.Success = false
	if args.Term == rf.currentTerm {
		if rf.state != Follower {
			rf.becomeFollower(args.Term)
		}
		rf.electionResetEvent = time.Now()
		reply.Success = true
	}

	reply.Term = rf.currentTerm
	rf.dlog("AppendEntries reply: %+v", *reply)
	return
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	return index, term, isLeader
}

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	rf.state = Follower
	rf.votedFor = -1

	go func() {
		rf.mu.Lock()
		rf.electionResetEvent = time.Now()
		rf.mu.Unlock()
		rf.runElectionTimer()
	}()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	return rf
}

// runElectionTimer implements an election timer. It should be launched whenever
// we want to start a timer towards becoming a candidate in a new election.
//
// This function is blocking and should be launched in a separate goroutine;
// it's designed to work for a single (one-shot) election timer, as it exits
// whenever the CM state changes from follower/candidate or the term changes.
func (rf *Raft) runElectionTimer() {
	timeoutDuration := rf.electionTimeout()
	rf.mu.Lock()
	termStarted := rf.currentTerm
	rf.mu.Unlock()
	rf.dlog("election timer started (%v), term=%d", timeoutDuration, termStarted)

	// This loops until either:
	// - we discover the election timer is no longer needed, or
	// - the election timer expires and this CM becomes a candidate
	// In a follower, this typically keeps running in the background for the
	// duration of the CM's lifetime.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C

		rf.mu.Lock()
		if rf.state != Candidate && rf.state != Follower {
			rf.dlog("in election timer state=%s, bailing out", rf.state)
			rf.mu.Unlock()
			return
		}

		if termStarted != rf.currentTerm {
			rf.dlog("in election timer term changed from %d to %d, bailing out",
				termStarted, rf.currentTerm)
			rf.mu.Unlock()
			return
		}

		// Start an election if we haven't heard from a leader or haven't voted for
		// someone for the duration of the timeout.
		if elapsed := time.Since(rf.electionResetEvent); elapsed >= timeoutDuration {
			rf.startElection()
			rf.mu.Unlock()
			return
		}
		rf.mu.Unlock()
	}
}

// electionTimeout generates a pseudo-random election timeout duration.
func (rf *Raft) electionTimeout() time.Duration {
	// If RAFT_FORCE_MORE_REELECTION is set, stress-test by deliberately
	// generating a hard-coded number very often. This will create collisions
	// between different servers and force more re-elections.
	if len(os.Getenv("RAFT_FORCE_MORE_REELECTION")) > 0 && rand.Intn(3) == 0 {
		return time.Duration(150) * time.Millisecond
	} else {
		return time.Duration(150+rand.Intn(150)) * time.Millisecond
	}
}

// startElection starts a new election with this CM as a candidate.
// Expects rf.mu to be locked.
func (rf *Raft) startElection() {
	rf.state = Candidate
	rf.currentTerm += 1
	savedCurrentTerm := rf.currentTerm
	rf.electionResetEvent = time.Now()
	rf.votedFor = rf.me
	rf.dlog("becomes Candidate (currentTerm=%d); log=%v", savedCurrentTerm, rf.log)

	var votesReceived int32 = 1

	// Send RequestVote RPCs to all other servers concurrently.
	for peerId := range rf.peers {
		if peerId == rf.me {
			continue
		}
		go func(peerId int) {
			args := RequestVoteArgs{
				Term:        savedCurrentTerm,
				CandidateId: rf.me,
			}
			var reply RequestVoteReply

			rf.dlog("sending RequestVote to %d: %+v", peerId, args)
			if rf.peers[peerId].Call("Raft.RequestVote", args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				rf.dlog("received RequestVoteReply %+v", reply)

				if rf.state != Candidate {
					rf.dlog("while waiting for reply, state = %v", rf.state)
					return
				}

				if reply.Term > savedCurrentTerm {
					rf.dlog("term out of date in RequestVoteReply")
					rf.becomeFollower(reply.Term)
					return
				} else if reply.Term == savedCurrentTerm {
					if reply.VoteGranted {
						votes := int(atomic.AddInt32(&votesReceived, 1))
						if votes*2 > len(rf.peers) {
							// Won the election!
							rf.dlog("wins election with %d votes", votes)
							rf.startLeader()
							return
						}
					}
				}
			}
		}(peerId)
	}

	// Run another election timer, in case this election is not successful.
	go rf.runElectionTimer()
}

// startLeader switches cm into a leader state and begins process of heartbeats.
// Expects rf.mu to be locked.
func (rf *Raft) startLeader() {
	rf.state = Leader
	rf.dlog("becomes Leader; term=%d, log=%v", rf.currentTerm, rf.log)

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		// Send periodic heartbeats, as long as still leader.
		for {
			rf.leaderSendHeartbeats()
			<-ticker.C

			rf.mu.Lock()
			if rf.state != Leader {
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()
		}
	}()
}

// leaderSendHeartbeats sends a round of heartbeats to all peers, collects their
// replies and adjusts cm's state.
func (rf *Raft) leaderSendHeartbeats() {
	rf.mu.Lock()
	savedCurrentTerm := rf.currentTerm
	rf.mu.Unlock()

	for peerId := range rf.peers {
		if peerId == rf.me {
			continue
		}
		args := AppendEntriesArgs{
			Term:     savedCurrentTerm,
			LeaderId: rf.me,
		}
		go func(peerId int) {
			rf.dlog("sending AppendEntries to %v: ni=%d, args=%+v", peerId, 0, args)
			var reply AppendEntriesReply
			if rf.peers[peerId].Call("Raft.AppendEntries", args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				if reply.Term > savedCurrentTerm {
					rf.dlog("term out of date in heartbeat reply")
					rf.becomeFollower(reply.Term)
					return
				}
			}
		}(peerId)
	}
}

// becomeFollower makes cm a follower and resets its state.
// Expects rf.mu to be locked.
func (rf *Raft) becomeFollower(term int) {
	rf.dlog("becomes Follower with term=%d; log=%v", term, rf.log)
	rf.state = Follower
	rf.currentTerm = term
	rf.votedFor = -1
	rf.electionResetEvent = time.Now()

	go rf.runElectionTimer()
}
